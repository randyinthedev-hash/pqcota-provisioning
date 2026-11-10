// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main_test

// TP-RECORD-4·5·6 — `--dsn` 경로를 **빌드한 명령으로, 실제 Postgres에서** 시험한다.
//
// 이 경로에는 시험이 없었다. 저장소 계약(TP-RECORD-3)은 저장소만 재서, 명령이 저장소를 어떻게
// 여는지(어느 조직으로, 어느 순서로, 실패하면 무엇이 남는지)는 아무도 재지 않았고, 그 사이에서
// 결함이 한 건 나왔다: 이력은 PQCOTA_ORG로 열면서 레코드 저장소는 조직 없이 열어, 조직을 쓰는
// 배포에서 롤백 근거가 `default` 조직에 쌓이고 `pqcota-records`가 그것을 못 보았다.
//
// PQCOTA_TEST_DSN이 있을 때만 돈다. 레코드는 추가 전용이라 지우지 않으므로, 실행마다 유일한 조직·
// 계획·노드 이름을 쓴다.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	commonv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/common/v1"
	discoveryv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/discovery/v1"
	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	"google.golang.org/protobuf/encoding/protojson"
)

// countRecords — 이 조직의 레코드 행 수를 DB에서 직접 센다. 읽기 전후의 수가 같음만 보여 주며,
// 행의 내용이 바뀌지 않았음까지는 보여 주지 않는다.
func countRecords(t *testing.T, dsn, organization string) int {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pqcota_provisioning_record WHERE org=$1`, organization).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func buildRecords(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "pqcota-records")
	cmd := exec.Command("go", "build", "-o", bin, "../pqcota-records")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build pqcota-records: %v\n%s", err, out)
	}
	return bin
}

func TestDSNPathKeepsRecordsInTheNamedOrganization(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	prov, recs := buildCLI(t), buildRecords(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	orgA, orgB := "pvt-a-"+suffix, "pvt-b-"+suffix
	planID, node := "pvt-plan-"+suffix, "pvt-node-"+suffix

	// 빈칸이 없는 계획에서 계획 id와 노드만 이 실행의 것으로 바꾼다. derivedFromSnapshotId는 이력에
	// 없는 값이라 --dsn에서는 「못 찾은 참조」가 되고, 그래서 종료 3이 난다(아래에서 따로 본다).
	plan := writePlan(t, strings.NewReplacer(`"t-ok"`, `"`+planID+`"`, `"n1"`, `"`+node+`"`).Replace(planOK))

	provision := func(env []string, extra ...string) (string, string, int) {
		args := append([]string{"--level", "l2", unverified, "--dsn", dsn}, extra...)
		return runProvision(t, prov, env, append(args, plan)...)
	}
	list := func(env []string, args ...string) string {
		out, errOut, code := runProvision(t, recs, append(env, "PQCOTA_DSN="+dsn), args...)
		if code != 0 {
			t.Fatalf("pqcota-records 종료 %d:\n%s", code, errOut)
		}
		return out
	}

	// TP-RECORD-5 — 조직 A로 쓰면 A에서만 보인다. 다른 조직과 조직 없는 조회에는 보이지 않는다.
	if _, errOut, code := provision(envWith("PQCOTA_ORG="+orgA), "--allow-incomplete"); code != 0 {
		t.Fatalf("조직 %s로 --dsn 실행이 실패했다(%d):\n%s", orgA, code, errOut)
	}
	if got := list(envWith("PQCOTA_ORG=" + orgA)); !strings.Contains(got, planID+":a1") {
		t.Errorf("조직 %s로 쓴 레코드가 그 조직에서 보이지 않는다(기본 조직에 쓰였나):\n%s", orgA, got)
	}
	if got := list(envWith("PQCOTA_ORG=" + orgB)); strings.Contains(got, planID) {
		t.Errorf("조직 %s에서 조직 %s의 레코드가 보인다:\n%s", orgB, orgA, got)
	}
	if got := list(envWith()); strings.Contains(got, planID) {
		t.Errorf("조직을 대지 않은 조회에서 조직 %s의 레코드가 보인다:\n%s", orgA, got)
	}

	// TP-RECORD-6 — 노드를 주면 그 노드만 나온다. 읽기 전후에 이 조직의 레코드 행 수가 같음을 확인한다.
	// 기존 행의 내용 변경이나, 행 수를 유지하는 쓰기(고치기, 지우고 다시 넣기)는 확인하지 않는다.
	rowsBefore := countRecords(t, dsn, orgA)
	byNode := list(envWith("PQCOTA_ORG="+orgA), node)
	if !strings.Contains(byNode, "provisioning records (1)") || !strings.Contains(byNode, "node="+node) {
		t.Errorf("노드를 주었는데 그 노드의 레코드 하나가 나오지 않았다:\n%s", byNode)
	}
	if again := list(envWith("PQCOTA_ORG="+orgA), node); again != byNode {
		t.Errorf("읽기가 결과를 바꿨다:\n%s\n---\n%s", byNode, again)
	}
	if none := list(envWith("PQCOTA_ORG="+orgA), "no-such-node-"+suffix); !strings.Contains(none, "provisioning records (0)") {
		t.Errorf("없는 노드인데 레코드가 나왔다:\n%s", none)
	}
	if rowsAfter := countRecords(t, dsn, orgA); rowsAfter != rowsBefore || rowsBefore != 1 {
		t.Errorf("읽기 전후의 행 수가 다르거나(%d → %d) 처음 실행이 남긴 한 건이 아니다", rowsBefore, rowsAfter)
	}

	// TP-RECORD-4 — 못 찾은 스냅샷 참조는 불완전(종료 3)이다. 그래도 레코드는 남는다.
	planB := strings.ReplaceAll(planID, "pvt-plan-", "pvt-plan-b-")
	_, errOut, code := runProvision(t, prov, envWith("PQCOTA_ORG="+orgB), "--level", "l2", unverified, "--dsn", dsn,
		writePlan(t, strings.NewReplacer(`"t-ok"`, `"`+planB+`"`, `"n1"`, `"`+node+`"`).Replace(planOK)))
	if code != 3 {
		t.Errorf("못 찾은 참조가 있는데 종료 %d (want 3):\n%s", code, errOut)
	}
	if !strings.Contains(errOut, "persisted 1 records") || !strings.Contains(errOut, "unresolved 1") {
		t.Errorf("저장한 수와 못 찾은 참조 수를 말하지 않는다:\n%s", errOut)
	}
	if got := list(envWith("PQCOTA_ORG=" + orgB)); !strings.Contains(got, planB+":a1") || !strings.Contains(got, "(unresolved)") {
		t.Errorf("불완전해도 레코드는 남고, 찾지 못한 참조가 레코드에 적혀야 한다:\n%s", got)
	}
}

// TP-RECORD-5 — 조직을 반드시 대야 하는 배포(PQCOTA_REQUIRE_ORG=1)에서, 조직을 대면 --dsn 경로가
// 열리고 대지 않으면 거절된다. 전에는 조직을 **댔는데도** 레코드 저장소가 열리지 않았다.
func TestDSNPathUnderRequiredOrganization(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	prov := buildCLI(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	plan := writePlan(t, strings.NewReplacer(`"t-ok"`, `"pvt-req-`+suffix+`"`, `"n1"`, `"pvt-reqnode-`+suffix+`"`).Replace(planOK))
	base := []string{"--level", "l2", unverified, "--allow-incomplete", "--dsn", dsn, plan}

	if _, errOut, code := runProvision(t, prov, envWith("PQCOTA_REQUIRE_ORG=1", "PQCOTA_ORG=pvt-req-"+suffix), base...); code != 0 {
		t.Errorf("조직을 댔는데 필수 모드에서 --dsn이 실패했다(%d):\n%s", code, errOut)
	}
	_, errOut, code := runProvision(t, prov, envWith("PQCOTA_REQUIRE_ORG=1"), base...)
	if code == 0 || !strings.Contains(errOut, "no organization was named") {
		t.Errorf("필수 모드에서 조직을 대지 않았는데 거절되지 않았다(%d):\n%s", code, errOut)
	}
}

// recordOf — 이 조직의 레코드 하나를 DB에서 읽는다.
func recordOf(t *testing.T, dsn, organization, id string) *provisioningv1.ProvisioningRecord {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT record FROM pqcota_provisioning_record WHERE org=$1 AND id=$2 ORDER BY seq DESC LIMIT 1`, organization, id).Scan(&raw); err != nil {
		t.Fatalf("레코드 %s를 읽지 못했다: %v", id, err)
	}
	r := &provisioningv1.ProvisioningRecord{}
	if err := protojson.Unmarshal(raw, r); err != nil {
		t.Fatal(err)
	}
	return r
}

func jcaFinding(id string, chain ...string) *discoveryv1.Finding {
	return &discoveryv1.Finding{Id: id, CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_JCA,
		RuntimeAxes: &discoveryv1.Finding_Jca{Jca: &discoveryv1.JcaAxes{ProviderSet: chain}}}
}

// 기록의 before는 조치의 finding 하나에서만 캡처한다. v0.10.6까지는 노드의 finding 전부를 합쳐, JVM이
// 여럿이면 다른 앱의 provider 체인이 이어 붙었다(2026-11-10 실제 JVM 넷으로 재현, 체인 25개).
// 최신 스냅샷에 그 finding이 없으면 비우고, 그렇다고 알리며, 다른 finding으로 대신 채우지 않는다.
func TestDSNPathCapturesOnlyTheActionsFinding(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	prov, recs := buildCLI(t), buildRecords(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	organization, node, gone := "pvt-cap-"+suffix, "pvt-cap-node-"+suffix, "pvt-cap-gone-"+suffix

	hist, err := history.NewPgStoreIn(context.Background(), dsn, organization)
	if err != nil {
		t.Fatal(err)
	}
	ssl := &discoveryv1.Finding{Id: "f-ssl", CryptoRuntime: commonv1.CryptoRuntime_CRYPTO_RUNTIME_OPENSSL,
		RuntimeAxes: &discoveryv1.Finding_Openssl{Openssl: &discoveryv1.OpensslAxes{Lib: "libssl.so.3", Version: "3.0.13"}}}
	a, b := jcaFinding("f-jca-a", "SUN", "BC"), jcaFinding("f-jca-b", "SUN", "SunJCE", "SunEC")
	for _, s := range []*history.Snapshot{
		{ID: "s1-" + suffix, NodeID: node, RulesetVersion: "pqcota-enrich/v3", Findings: []*discoveryv1.Finding{ssl, a, b}},
		// gone 노드: 근거 스냅샷에는 f-jca-a가 있고, 그 뒤의 최신 스냅샷에는 없다(다른 finding만 있다)
		{ID: "g1-" + suffix, NodeID: gone, RulesetVersion: "pqcota-enrich/v3", Findings: []*discoveryv1.Finding{jcaFinding("f-jca-a", "SUN", "BC")}},
		{ID: "g2-" + suffix, NodeID: gone, RulesetVersion: "pqcota-enrich/v3", Findings: []*discoveryv1.Finding{jcaFinding("f-jca-other", "SUN", "SunEC")}},
	} {
		if err := hist.Append(s); err != nil {
			t.Fatal(err)
		}
	}
	hist.Close()

	action := func(id, target, snap string) string {
		return `{"id":"` + id + `","targetNodeId":"` + target + `","findingId":"f-jca-a",
      "cryptoRuntime":"CRYPTO_RUNTIME_JCA","kind":"REMEDIATION_KIND_PROVIDER_INJECT",
      "providerChoice":"BC","providerClass":"org.bouncycastle.jce.provider.BouncyCastleProvider",
      "targetAlgorithm":"ML-KEM (FIPS 203)","automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL",
      "evidenceSources":[{"findingId":"f-jca-a","snapshot":{"sourceNodeId":"` + target + `","snapshotId":"` + snap + `"}}]}`
	}
	planID := "pvt-cap-plan-" + suffix
	plan := writePlan(t, `{"id":"`+planID+`","status":"PLAN_STATUS_FINALIZED","scope":"ring-0",
  "approvalSignatures":["reviewer:test"],"derivedFromSnapshotId":"s1-`+suffix+`","rulesetVersion":"rs-1",
  "finalizedAt":"2026-11-10T00:00:00Z",
  "actions":[`+action("a1", node, "s1-"+suffix)+`,`+action("a2", gone, "g1-"+suffix)+`]}`)

	_, errOut, code := runProvision(t, prov, envWith("PQCOTA_ORG="+organization),
		"--level", "l2", unverified, "--allow-incomplete", "--dsn", dsn, plan)
	if code != 0 {
		t.Fatalf("--dsn 실행이 실패했다(%d):\n%s", code, errOut)
	}

	// 경우 1 — 대상 finding 하나만. 같은 노드의 OpenSSL 모듈도, 다른 JVM의 체인도 섞이지 않는다.
	got := recordOf(t, dsn, organization, planID+":a1").GetBefore()
	if chain := strings.Join(got.GetProviderChain(), ","); chain != "SUN,BC" {
		t.Errorf("a1의 before 체인이 대상 finding의 것이 아니다: %s (want SUN,BC)", chain)
	}
	if mods := strings.Join(got.GetModules(), ","); mods != "jca:BC,jca:SUN" {
		t.Errorf("a1의 before 모듈에 다른 finding이 섞였다: %s (want jca:BC,jca:SUN)", mods)
	}

	// 경우 2 — 근거 스냅샷 참조는 해결되지만 최신 스냅샷에 finding이 없다. 비우고, 알리고, 대신 채우지 않는다.
	r2 := recordOf(t, dsn, organization, planID+":a2")
	if len(r2.GetBefore().GetModules()) > 0 || len(r2.GetBefore().GetProviderChain()) > 0 {
		t.Errorf("최신 스냅샷에 없는 finding인데 before가 다른 값으로 채워졌다: %v", r2.GetBefore())
	}
	if len(r2.GetSnapshotResolutions()) != 1 || r2.GetSnapshotResolutions()[0].GetResolvedSnapshotId() == "" {
		t.Errorf("근거 스냅샷 참조가 해결되지 않았다(이 경우의 전제가 깨졌다): %v", r2.GetSnapshotResolutions())
	}
	if !strings.Contains(r2.GetNote(), "before not captured") {
		t.Errorf("기록에 캡처하지 못했다는 note가 없다: %q", r2.GetNote())
	}
	if !strings.Contains(errOut, "action a2 (node="+gone+"): finding f-jca-a is not in the node's latest snapshot") ||
		!strings.Contains(errOut, "before not captured 1") {
		t.Errorf("최신 스냅샷에서 finding을 못 찾았다는 경고나 그 수가 없다:\n%s", errOut)
	}
	if strings.Contains(errOut, "action a1 (node="+node+"): finding") {
		t.Errorf("찾은 finding에도 경고가 났다:\n%s", errOut)
	}
	out, recErr, rc := runProvision(t, recs, envWith("PQCOTA_ORG="+organization, "PQCOTA_DSN="+dsn), gone)
	if rc != 0 || !strings.Contains(out, "before : (nothing captured — not evidence that the app had no crypto configuration)") {
		t.Errorf("pqcota-records가 빈 before를 「캡처한 것 없음」으로 밝히지 않는다(%d):\n%s%s", rc, out, recErr)
	}
}

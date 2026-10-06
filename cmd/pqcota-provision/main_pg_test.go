// SPDX-FileCopyrightText: 2026 randyinthedev
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

// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence"
	sestore "github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/store"

	"github.com/jackc/pgx/v5/pgxpool"
)

// recordJSON — 그 조직의 기록 하나를 공백 없는 JSON으로(JSONB는 「: 」로 낸다).
func recordJSON(t *testing.T, dsn, organization, id string) string {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var raw string
	if err := pool.QueryRow(context.Background(), `SELECT record::text FROM pqcota_provisioning_record WHERE org=$1 AND id=$2`, organization, id).Scan(&raw); err != nil {
		t.Fatalf("record %s: %v", id, err)
	}
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

const pgCBOM = `{"bomFormat":"CycloneDX","specVersion":"1.6","serialNumber":"urn:uuid:STAMP","components":[
 {"type":"cryptographic-asset","name":"RSA","cryptoProperties":{"assetType":"algorithm"},
  "evidence":{"occurrences":[{"location":"src/Wrap.java","line":12}]}}]}`

// storeEvidence — 그 조직에 소스 근거 하나를 저장하고 변환 결과를 돌려준다.
func storeEvidence(t *testing.T, dsn, organization, stamp string) *sourceevidence.Interpretation {
	t.Helper()
	raw := []byte(strings.Replace(pgCBOM, "STAMP", stamp, 1))
	in, err := sourceevidence.Convert(raw, sourceevidence.DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	st, err := sestore.NewPgStoreIn(context.Background(), dsn, organization)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Save(sestore.Input{Raw: raw}, in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestSourceEvidenceWithDSN(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	bin := buildCLI(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	orgName := "pvt-src-" + stamp
	in := storeEvidence(t, dsn, orgName, stamp)
	item, loc := in.Items[0].ID, in.Items[0].Locations[0].ID
	env := envWith("PQCOTA_ORG=" + orgName)
	planWith := func(id, body string) string {
		return writePlan(t, strings.Replace(body, `"t-src"`, `"`+id+`"`, 1))
	}

	// 1. 조치 둘 가운데 소스 근거만 있는 a2가 미해결 — stdout이 비고 기록이 한 건도 없다(앞의 a1 기록도).
	before := countRecords(t, dsn, orgName)
	for _, extra := range [][]string{nil, {"--rollback"}} {
		p := planWith("src-miss-"+stamp, srcPlan(in.ID, "no-such-item", in.InputSHA256))
		args := append([]string{"--level", "l2", unverified, "--dsn", dsn}, extra...)
		out, errOut, code := runProvision(t, bin, env, append(args, p)...)
		if code != 1 || out != "" || !strings.Contains(errOut, "has no item no-such-item") {
			t.Errorf("%v: exit %d, stdout %d bytes\n%s", extra, code, len(out), errOut)
		}
	}
	if n := countRecords(t, dsn, orgName); n != before {
		t.Fatalf("a refused run left %d records", n-before)
	}
	// 다른 조직에서는 같은 참조가 찾히지 않는다.
	p := planWith("src-org-"+stamp, srcPlan(in.ID, item, in.InputSHA256))
	if out, errOut, code := runProvision(t, bin, envWith("PQCOTA_ORG=other-"+stamp), "--level", "l2", unverified, "--dsn", dsn, p); code != 1 || out != "" {
		t.Errorf("another organization: exit %d\n%s", code, errOut)
	}

	// 2. 해석되면 플레이북에 보고된 위치가 붙고, a2 기록은 before 「해당 없음」과 해석 결과를 남긴다.
	planID := "src-ok-" + stamp
	p = planWith(planID, strings.Replace(srcPlan(in.ID, item, in.InputSHA256), `"itemId":"`+item+`"`, `"itemId":"`+item+`","locationId":"`+loc+`"`, 1))
	out, errOut, code := runProvision(t, bin, env, "--level", "l2", unverified, "--dsn", dsn, "--allow-incomplete", p)
	if code != 0 || !strings.Contains(out, "# action a2   [0] RSA (/components/0)") || !strings.Contains(out, "reported location src/Wrap.java:12") {
		t.Fatalf("resolved: exit %d\n%s\n%s", code, out, errOut)
	}
	for _, line := range strings.Split(errOut, "\n") {
		if strings.Contains(line, "action a2") {
			t.Errorf("a source-only action got a runtime warning (before capture / snapshot reference): %s", line)
		}
	}
	if !strings.Contains(errOut, "before not captured 1") {
		t.Errorf("only a1 should count as an uncaptured before:\n%s", errOut)
	}
	rec := recordJSON(t, dsn, orgName, planID+":a2")
	if !strings.Contains(rec, `"resolved":true`) || !strings.Contains(rec, "before not applicable") {
		t.Errorf("record of a2: %s", rec)
	}
	if r1 := recordJSON(t, dsn, orgName, planID+":a1"); strings.Contains(r1, "sourceEvidenceResolutions") {
		t.Errorf("a record without source evidence gained the field: %s", r1)
	}

	// 3. 혼합 근거 조치(a1에 런타임 근거 + 미해결 소스 근거) — 런타임 생성은 나오고, 소스 안내는 빠지고, 사유가 남고, 불완전.
	mixed := strings.Replace(planOK, `"targetAlgorithm":"ML-KEM (FIPS 203)",`,
		`"targetAlgorithm":"ML-KEM (FIPS 203)","sourceEvidence":[{"interpretationId":"`+in.ID+`","itemId":"gone","inputSha256":"`+in.InputSHA256+`"}],`, 1)
	mixedID := "src-mixed-" + stamp
	p = writePlan(t, strings.Replace(mixed, `"t-ok"`, `"`+mixedID+`"`, 1))
	out, errOut, code = runProvision(t, bin, env, "--level", "l2", unverified, "--dsn", dsn, p)
	if code != 3 || !strings.Contains(out, "place the config fragment") || !strings.Contains(out, "# action a1   [0] not resolved: interpretation") ||
		strings.Contains(out, "reported location src/") || !strings.Contains(errOut, "source_evidence[0] not resolved") {
		t.Fatalf("mixed: exit %d\n%s\n%s", code, out, errOut)
	}
	if r := recordJSON(t, dsn, orgName, mixedID+":a1"); !strings.Contains(r, "has no item gone") || strings.Contains(r, `"resolved":true`) {
		t.Errorf("mixed record: %s", r)
	}

	// 4. id가 같은 두 조치(a1) — 첫째는 런타임 근거만, 둘째는 소스 근거만. 기록은 각자의 것을 갖는다.
	dupID := "src-dup-" + stamp
	dup := strings.Replace(srcPlan(in.ID, item, in.InputSHA256), `"id":"a2"`, `"id":"a1"`, 1)
	p = writePlan(t, strings.Replace(dup, `"t-src"`, `"`+dupID+`"`, 1))
	out, errOut, code = runProvision(t, bin, env, "--level", "l2", unverified, "--dsn", dsn, "--allow-incomplete", p)
	if code != 0 || strings.Count(out, "source evidence (reported locations") != 1 {
		t.Fatalf("duplicate ids: exit %d\n%s\n%s", code, out, errOut)
	}
	recs := recordsOfPlan(t, dsn, orgName, dupID)
	if len(recs) != 2 || strings.Contains(recs[0], "sourceEvidenceResolutions") || !strings.Contains(recs[1], `"resolved":true`) ||
		!strings.Contains(recs[1], "before not applicable") || strings.Contains(recs[0], "before not applicable") {
		t.Fatalf("records of two actions sharing an id are mixed:\n%s", strings.Join(recs, "\n"))
	}
}

// 소스 근거만 있는 조치의 가이드: 맞으면 출력·기록에 대조 결과, 틀리면 계획 전체를 출력 전에 거부(기록 0).
// 런타임 근거를 고른 가이드는 --dsn으로 이력을 출력 전에 조회하고, 못 찾으면 가이드만 빠진다.
func TestGuideWithDSN(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	bin := buildCLI(t)
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	orgName := "pvt-guide-" + stamp
	in := storeEvidence(t, dsn, orgName, stamp)
	item := in.Items[0].ID
	env := envWith("PQCOTA_ORG=" + orgName)
	dir, body, man := guideCatalog(t)
	srcOnly := func(id, b string) string {
		p := strings.Replace(srcPlan(in.ID, item, in.InputSHA256), `"inputSha256":"`+in.InputSHA256+`"}]}`,
			`"inputSha256":"`+in.InputSHA256+`"}],`+guideJSON(b, man, `,"sourceEvidenceIndexes":[0]`)+`}`, 1)
		return writePlan(t, strings.Replace(p, `"t-src"`, `"`+id+`"`, 1))
	}

	okID := "g-ok-" + stamp
	out, errOut, code := runProvision(t, bin, env, "--level", "l2", unverified, "--dsn", dsn, "--guide-dir", dir, "--allow-incomplete", srcOnly(okID, body))
	if code != 0 || !strings.Contains(out, "# action a2 guide named in the plan") || !strings.Contains(out, "chosen for: [source 0] RSA (/components/0)") {
		t.Fatalf("source-only guide: exit %d\n%s\n%s", code, out, errOut)
	}
	recs := recordsOfPlan(t, dsn, orgName, okID)
	if len(recs) != 2 || strings.Contains(recs[0], "guideResolution") || !strings.Contains(recs[1], `"guideResolution":{`) ||
		!strings.Contains(recs[1], `"resolved":true`) || strings.Contains(recs[1], `"approvalsVerified":true`) {
		t.Fatalf("records:\n%s", strings.Join(recs, "\n"))
	}

	badID := "g-bad-" + stamp
	for _, extra := range [][]string{nil, {"--rollback"}} {
		args := append([]string{"--level", "l2", unverified, "--dsn", dsn, "--guide-dir", dir}, extra...)
		out, errOut, code = runProvision(t, bin, env, append(args, srcOnly(badID, strings.Repeat("0", 64)))...)
		if code != 1 || out != "" || !strings.Contains(errOut, "names a guide that does not resolve") || !strings.Contains(errOut, "body hash differs") {
			t.Fatalf("%v bad guide: exit %d, stdout %d bytes\n%s", extra, code, len(out), errOut)
		}
	}
	if n := len(recordsOfPlan(t, dsn, orgName, badID)); n != 0 {
		t.Fatalf("a refused run left %d records", n)
	}

	// 런타임 근거를 고른 가이드 — 이력에 그 스냅샷이 없다 → 가이드만 빠지고 불완전, 사유가 기록에.
	rtID := "g-rt-" + stamp
	rt := strings.Replace(planWithGuide(body, man, `,"evidenceSourceIndexes":[0]`), `"automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL"}`,
		`"automationLevel":"DEPLOY_AUTOMATION_LEVEL_L2_STAGE_INSTALL","evidenceSources":[{"findingId":"f1","snapshot":{"sourceNodeId":"n1","snapshotId":"no-such-`+stamp+`"}}]}`, 1)
	out, errOut, code = runProvision(t, bin, env, "--level", "l2", unverified, "--dsn", dsn, "--guide-dir", dir, writePlan(t, strings.Replace(rt, `"t-ok"`, `"`+rtID+`"`, 1)))
	if code != 3 || !strings.Contains(out, "guide not given") || !strings.Contains(errOut, "[runtime 0]) did not resolve") {
		t.Fatalf("runtime evidence: exit %d\n%s\n%s", code, out, errOut)
	}
	if r := recordsOfPlan(t, dsn, orgName, rtID); len(r) != 1 || !strings.Contains(r[0], "did not resolve") {
		t.Fatalf("runtime record: %v", r)
	}
}

// recordsOfPlan — 한 계획의 기록을 쓴 순서대로(공백 없는 JSON).
func recordsOfPlan(t *testing.T, dsn, organization, planID string) []string {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := pool.Query(context.Background(), `SELECT record::text FROM pqcota_provisioning_record WHERE org=$1 AND plan_id=$2 ORDER BY seq`, organization, planID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		out = append(out, b.String())
	}
	return out
}

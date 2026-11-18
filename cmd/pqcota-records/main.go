// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

// Command pqcota-records — 영속된 프로비저닝 레코드(롤백 근거)를 조회한다(프로비저닝 설계 §6A).
// pqcota-provision이 append-only로 남긴 before/after·영향 앱·상태를 읽기전용으로 나열한다.
// 이 뷰는 "무엇이 언제 어떤 before로 스테이징됐나"만 보인다.
//
// usage: pqcota-records [node]        (env PQCOTA_DSN 필수 — pqcota-provision과 같은 저장소)
//
//	pqcota-records -init-schema   # 프로비저닝 기록 표를 만들고 끝난다(한 트랜잭션)
//
// 조회는 DDL을 돌리지 않는다(v0.16.0부터). v0.15.0까지는 PQCOTA_AUTO_DDL=0이어도 빈 DB에 표를 만들었다.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/pgschema"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/org"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

func main() {
	initSchema := flag.Bool("init-schema", false, "create the provisioning-record table in one transaction and exit; run it as the schema owner, and again after each upgrade")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: pqcota-records [node] | pqcota-records -init-schema")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *initSchema && flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "-init-schema only creates the schema; it does not take a node argument")
		os.Exit(2)
	}
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}
	dsn := os.Getenv("PQCOTA_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "PQCOTA_DSN is required — the Postgres that pqcota-provision wrote records to.")
		os.Exit(2)
	}
	if *initSchema {
		os.Exit(initialize(dsn))
	}
	// 조회는 DDL 없이 연다 — 빈 DB에 표를 만들지 않고, 읽기 권한만 있는 계정으로도 열린다.
	store, err := provisioning.OpenPgRecordStoreIn(context.Background(), dsn, org.FromEnv())
	if err != nil {
		fmt.Fprintln(os.Stderr, "opening the provisioning-record store:", err)
		os.Exit(1)
	}
	defer store.Close()

	var recs []*provisioningv1.ProvisioningRecord
	if node := flag.Arg(0); node != "" {
		recs, err = store.ByNode(node)
	} else {
		recs, err = store.All()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "query:", err)
		os.Exit(1)
	}

	fmt.Printf("═══ provisioning records (%d) ═══\n", len(recs))
	for _, r := range recs {
		fmt.Printf("• %s  [%s]  node=%s  plan=%s\n", r.GetId(),
			short(r.GetStatus().String(), "PROVISIONING_STATUS_"), r.GetNodeId(), r.GetPlanId())
		if ak := r.GetAppKeys(); len(ak) > 0 {
			fmt.Printf("    affected apps: %s\n", strings.Join(ak, ", "))
		}
		if b := r.GetBefore(); b != nil && len(b.GetModules()) > 0 {
			fmt.Printf("    before : %s\n", strings.Join(b.GetModules(), ", "))
		} else {
			// 빈 before는 캡처하지 못한 것이다. 「그 앱에 암호 모듈이 없었다」로 읽히지 않게 그렇게 적는다.
			fmt.Printf("    before : (nothing captured — not evidence that the app had no crypto configuration)\n")
		}
		// 어느 스냅샷 상태에서 나온 조치인지 — 되짚는 사슬의 마지막 고리. 찾은 것과 못 찾은 것을 가려 낸다.
		for _, sr := range r.GetSnapshotResolutions() {
			if id := sr.GetResolvedSnapshotId(); id != "" {
				fmt.Printf("    snapshot: %s\n", id)
			} else {
				fmt.Printf("    snapshot: (unresolved) %s\n", sr.GetReason())
			}
		}
		if a := r.GetAfter(); a != nil && len(a.GetModules()) > 0 {
			fmt.Printf("    after  : %s\n", strings.Join(a.GetModules(), ", "))
		}
		if r.GetNote() != "" {
			fmt.Printf("    note   : %s\n", r.GetNote())
		}
	}
}

func short(s, prefix string) string { return strings.TrimPrefix(s, prefix) }

// initialize — -init-schema. 성공 줄은 커밋하고 표·열을 다시 확인한 뒤에만 낸다.
func initialize(dsn string) int {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connecting to Postgres:", err)
		return 1
	}
	defer pool.Close()
	rep, err := pgschema.Init(ctx, pool, provisioning.RecordSchema())
	if err != nil {
		fmt.Fprintln(os.Stderr, "initializing the schema failed:", err)
		if pgschema.IsPrivilegeError(err) {
			fmt.Fprintln(os.Stderr, "(this account may not own the existing pqcota table, or lacks USAGE/CREATE on the schema. "+
				"Run -init-schema as the role that owns it, or move its ownership to the schema role first: "+
				"https://github.com/randyinthedev-hash/pqcota-inventory/blob/main/cmd/README.md#setting-up-the-database)")
		}
		return 1
	}
	fmt.Print(pgschema.FormatReport(rep))
	return 0
}

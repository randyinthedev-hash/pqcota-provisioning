// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

// Command pqcota-records — 영속된 프로비저닝 레코드(롤백 근거)를 조회한다(프로비저닝 설계 §6A).
// pqcota-provision이 append-only로 남긴 before/after·영향 앱·상태를 읽기전용으로 나열한다.
// 이 뷰는 "무엇이 언제 어떤 before로 스테이징됐나"만 보인다.
//
// usage: pqcota-records [node]        (env PQCOTA_DSN 필수 — pqcota-provision과 같은 저장소)
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/org"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

func main() {
	dsn := os.Getenv("PQCOTA_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "PQCOTA_DSN is required — the Postgres that pqcota-provision wrote records to.")
		os.Exit(2)
	}
	store, err := provisioning.NewPgRecordStoreIn(context.Background(), dsn, org.FromEnv())
	if err != nil {
		fmt.Fprintln(os.Stderr, "connecting to Postgres:", err)
		os.Exit(1)
	}
	defer store.Close()

	var recs []*provisioningv1.ProvisioningRecord
	if len(os.Args) > 1 && os.Args[1] != "" {
		recs, err = store.ByNode(os.Args[1])
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

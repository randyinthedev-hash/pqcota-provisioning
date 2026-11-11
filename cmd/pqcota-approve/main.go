// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

// Command pqcota-approve — 계획에 **승인 서명**을 붙이고, 첫 승인이면 상태를 FINALIZED로 올린다
// (§3.3③ finalize 전제).
//
// 판정과 실행 승인은 다른 단계다. 판정을 끝낸 쪽은 IN_REVIEW로 넘기고, 이 명령이 FINALIZED로
// 올린다. 상태별 동작은 provisioning.PrepareApproval에 있다 — DRAFT는 거부하고, IN_REVIEW는
// 승인이 없어야 하며, FINALIZED는 승인이 있어야 한다. 그러고 나서 계획의 내용 전부(승인 서명
// 자신만 빼고)를 ed25519로 서명해 `approval_signatures`에 덧붙이고, 계획을 다시 낸다. 승인자
// id가 서명 문자열 안에 들어가므로, 검증하는 쪽은 **그 사람의 키로만** 확인한다
// (pqcota-provision의 PQCOTA_APPROVAL_KEYS).
//
// 상태와 확정 시각은 **서명 전에** 정한다. 서명이 그 둘을 덮으므로, 뒤에 바꾸면 방금 만든
// 서명이 깨진다. 두 번째 승인부터는 아무것도 바꾸지 않고 서명만 더한다.
//
// usage: pqcota-approve --approver <id> [--dsn <postgres-dsn>] <plan.json>
//
//	--approver <id>          : 승인자 id. ':'는 쓸 수 없다(서명 문자열의 구분자)
//	--dsn <dsn>              : 소스 근거 저장소(조회 전용). 소스 근거만 있는 조치가 있으면 필수 — 서명 전에 해석한다
//	env PQCOTA_APPROVAL_KEY  : base64 ed25519 **개인키**. pqcota-keygen이 낸 것
//
// 서명한 계획은 stdout으로 나간다. 원본을 덮어쓰지 않는 이유는, 승인 전 계획과 승인된 계획이
// 같은 파일이면 무엇에 서명했는지 되짚을 수 없기 때문이다.
//
// ★ 서명한 뒤에 계획을 고치면 **승인은 무효가 된다**. 그것이 요점이다: 승인자는 계획의 이름이
// 아니라 조치의 내용에 책임을 진다. 조각을 나중에 채우려면(FillPlan) 채운 뒤에 승인받는다.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
	"github.com/randyinthedev-hash/pqcota-common/pkg/org"
	sestore "github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/sourceevidence/store"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
	"google.golang.org/protobuf/encoding/protojson"
)

func main() {
	approver := flag.String("approver", "", "approver id — it goes inside the signature so verification is bound to this person")
	dsn := flag.String("dsn", "", "Postgres DSN of the source evidence store (read-only); required when an action is based only on source evidence")
	flag.Parse()
	if flag.NArg() < 1 || *approver == "" {
		fmt.Fprintln(os.Stderr, "usage: pqcota-approve --approver <id> [--dsn <postgres-dsn>] <plan.json>   (env PQCOTA_APPROVAL_KEY = base64 ed25519 private key)")
		os.Exit(2)
	}

	key := os.Getenv("PQCOTA_APPROVAL_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "PQCOTA_APPROVAL_KEY is not set — there is no key to approve with. Generate one with pqcota-keygen.")
		os.Exit(2)
	}

	raw, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading the plan:", err)
		os.Exit(1)
	}
	plan := &provisioningv1.FinalizedPlan{}
	if err := protojson.Unmarshal(raw, plan); err != nil {
		fmt.Fprintln(os.Stderr, "parsing the plan:", err)
		os.Exit(1)
	}

	// 승인 대상인지 보고, 첫 승인이면 상태를 올린다. **서명보다 먼저** — 서명이 상태를 덮는다.
	first := plan.GetStatus() == provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW
	if err := provisioning.PrepareApproval(plan, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "refusing to approve:", err)
		os.Exit(1)
	}

	// 소스 근거 — 서명 **전에** 해석한다. 소스 근거만 있는 조치는 그것이 유일한 근거라, 하나라도 못 찾으면
	// 서명하지 않는다(승인자가 보지 못한 근거에 책임을 질 수 없다). 그런 조치가 있으면 저장소 연결이 필요하다 —
	// 없으면 해석할 수 없으므로 같은 거부다. 혼합 근거 조치의 미해결은 알리기만 한다.
	if provisioning.PlanHasSourceEvidence(plan) {
		var lookup provisioning.SourceEvidenceLookup
		if *dsn != "" {
			st, err := sestore.OpenPgStoreIn(context.Background(), *dsn, org.FromEnv())
			if err != nil {
				lookup = provisioning.FailingSourceLookup{Err: fmt.Errorf("could not open the source evidence store: %w", err)}
			} else {
				defer st.Close()
				lookup = st
			}
		}
		check, err := provisioning.CheckSourceEvidence(lookup, plan)
		if err != nil {
			fmt.Fprintln(os.Stderr, "refusing to approve:", err)
			if *dsn == "" {
				fmt.Fprintln(os.Stderr, "(an action based only on source evidence must resolve before it is approved — give --dsn for the source evidence store)")
			}
			os.Exit(1)
		}
		for _, w := range check.Incomplete(plan) {
			fmt.Fprintln(os.Stderr, "⚠ [approve] "+w)
		}
	}

	sig, err := sign.SignApproval(key, *approver, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signing:", err)
		os.Exit(1)
	}
	plan.ApprovalSignatures = append(plan.ApprovalSignatures, sig)

	out, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "writing the plan:", err)
		os.Exit(1)
	}
	os.Stdout.Write(out)
	what := "added an approval to"
	if first {
		what = "finalized"
	}
	fmt.Fprintf(os.Stderr, "[approve] %s %s plan %q (%d actions, %d approval(s)). The approval covers every field except the signatures themselves — editing the plan after this invalidates it.\n",
		*approver, what, plan.GetId(), len(plan.GetActions()), len(plan.GetApprovalSignatures()))
}

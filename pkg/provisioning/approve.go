// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning

import (
	"errors"
	"fmt"
	"time"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrNotApprovable — 승인할 대상이 아닌 계획. DRAFT·UNSPECIFIED, 또는 승인 전에 물을 수 있는
// 구조를 갖추지 못한 계획이다.
var ErrNotApprovable = errors.New("plan cannot be approved")

// ErrCorruptPlan — 상태가 말하는 것과 계획에 있는 것이 어긋난다. IN_REVIEW인데 승인이 달려
// 있거나, FINALIZED인데 승인이나 확정 시각이 없다. 누군가 상태만 바꿔 넣었거나 필드를 지운 것이라,
// 어느 쪽이든 **이 계획이 무엇인지 말할 수 없다.** 거절 말고는 할 수 있는 것이 없다.
var ErrCorruptPlan = errors.New("plan is inconsistent with its own status")

// PrepareApproval — 승인 서명을 붙이기 **직전에** 계획이 승인 대상인지 보고, 승인이 만드는
// 상태 전이를 적용한다. 서명은 이 함수가 돌려준 뒤에 한다 — CanonicalPlan이 status와
// finalized_at을 덮으므로, 서명 뒤에 바꾸면 방금 만든 서명이 깨진다.
//
// 판정과 실행 승인은 다른 단계다. 판정을 끝낸 쪽은 IN_REVIEW로 넘기고, 승인이 FINALIZED로
// 올린다. 계약에 이미 그 둘이 있어 새 상태는 필요 없다. 상태별로:
//
//   - IN_REVIEW: 기존 승인과 finalized_at이 **없어야 한다.** 있으면 손상이다 — 누군가 이 상태에
//     서명을 달아 둔 것이고, 그 서명은 이 상태에 대한 것이다. 구조 검사를 지나면 상태를
//     FINALIZED로, finalized_at을 now로 정한다.
//   - FINALIZED: 기존 승인과 finalized_at을 **모두 갖춰야 한다.** 하나라도 없으면 손상이다 —
//     FINALIZED는 승인이 찍힌 뒤에만 생기는 상태라, 비어 있으면 상태만 바꿔 넣은 것이다.
//     갖췄으면 아무것도 바꾸지 않는다. 두 번째 승인자가 첫 번째와 **같은 정준 바이트**에
//     서명해야 둘 다 검증되기 때문이다.
//   - DRAFT·UNSPECIFIED: 승인할 대상이 아니다.
//
// 순서는 불변식 → 구조 → 전이다. 구조 검사(Executable의 내용 층, actionable)는 **두 상태 모두**
// 지난다. 여기서 걸리는 계획에 서명이 붙으면 승인은 됐는데 실행할 수 없는 계획이 생기고, 그것은
// 첫 승인이든 추가 승인이든 마찬가지다. 거부된 계획은 상태·시각·승인 목록이 들어온 그대로다.
//
// 전에는 pqcota-approve가 상태를 아예 보지 않았다. DRAFT 계획에도 서명이 찍혔다. 뒤에서
// Executable이 막아 악용되지는 않았지만, 승인이라는 행위가 무엇에 대한 것인지 확인하지 않는
// 자리였다.
//
// GATE: 배선 필수
func PrepareApproval(p *provisioningv1.FinalizedPlan, now time.Time) error {
	if p == nil {
		return fmt.Errorf("%w: nil", ErrNotApprovable)
	}
	// 1. 상태별 불변식. 상태가 말하는 것과 계획에 있는 것이 맞나.
	switch p.GetStatus() {
	case provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW:
		if n := len(p.GetApprovalSignatures()); n > 0 {
			return fmt.Errorf("%w: status is IN_REVIEW but %d approval(s) are already attached — an approval on an unapproved status is not an approval of this plan", ErrCorruptPlan, n)
		}
		if p.GetFinalizedAt() != nil {
			return fmt.Errorf("%w: status is IN_REVIEW but finalized_at is set — only approval sets it", ErrCorruptPlan)
		}
	case provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED:
		if len(p.GetApprovalSignatures()) == 0 {
			return fmt.Errorf("%w: status is FINALIZED but no approval is attached — FINALIZED only comes from an approval", ErrCorruptPlan)
		}
		if p.GetFinalizedAt() == nil {
			return fmt.Errorf("%w: status is FINALIZED but finalized_at is empty — approval sets it, so something removed it", ErrCorruptPlan)
		}
	default:
		return fmt.Errorf("%w: status=%s — only IN_REVIEW (first approval) or FINALIZED (further approvals) can be approved", ErrNotApprovable, p.GetStatus())
	}
	// 2. 구조. **두 상태 모두** 묻는다. FINALIZED에서 건너뛰면, 첫 승인 뒤에 조치가 지워지거나
	// 대상 노드·종류가 비워진 계획에도 추가 승인이 붙는다. 첫 서명은 이미 깨져 있겠지만, 그
	// 위에 새 서명을 얹는 것은 「실행할 수 없는 계획에는 승인하지 않는다」와 어긋난다.
	if len(p.GetActions()) == 0 {
		return fmt.Errorf("%w: no actions — there is nothing to approve", ErrNotApprovable)
	}
	if err := actionable(p); err != nil {
		return fmt.Errorf("%w: %v", ErrNotApprovable, err)
	}
	// 3. 전이. 첫 승인만 상태를 올린다. 여기까지 와서야 계획을 바꾸므로, 위에서 거부된 계획은
	// 상태·시각·승인 목록이 들어온 그대로다.
	if p.GetStatus() == provisioningv1.PlanStatus_PLAN_STATUS_IN_REVIEW {
		p.Status = provisioningv1.PlanStatus_PLAN_STATUS_FINALIZED
		p.FinalizedAt = timestamppb.New(now.UTC())
	}
	return nil
}

package provisioning_test

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// TP-RECORD-3 — 롤백 근거는 append-only로 남아야 되찾을 수 있다. 인메모리로 고정한 계약이
// Postgres에서도 같은지 본다. 저장소를 바꿨더니 순서가 섞이거나 노드가 새면, 되돌릴 때
// 무엇으로 돌아가야 하는지를 잘못 짚는다.
//
// PQCOTA_TEST_DSN이 있을 때만 실행. 기본 `go test`는 스킵한다.
func TestPgRecordStore(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	ctx := context.Background()
	st, err := provisioning.NewPgRecordStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// append-only라 지우지 않는다 — 실행마다 유니크 노드를 쓴다.
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	node, other := "cmdb://pgrec-"+suffix, "cmdb://pgrec-other-"+suffix

	rec := func(id, n string, apps ...string) *provisioningv1.ProvisioningRecord {
		return &provisioningv1.ProvisioningRecord{
			Id: id, NodeId: n, AppKeys: apps,
			Status: provisioningv1.ProvisioningStatus_PROVISIONING_STATUS_STAGED,
			Before: &provisioningv1.CryptoState{
				Modules:       []string{"libcrypto.so.3@3.0.13"},
				ProviderChain: []string{"SunJCE", "BC"},
			},
		}
	}
	for _, r := range []*provisioningv1.ProvisioningRecord{
		rec("r1-"+suffix, node, "pay.service"),
		rec("r2-"+suffix, node, "api.service"),
		rec("r3-"+suffix, other),
	} {
		if err := st.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.ByNode(node)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%[2]d records for %[1]s (want 2) — node isolation failed", node, len(got))
	}
	if got[0].GetId() != "r1-"+suffix || got[1].GetId() != "r2-"+suffix {
		t.Errorf("the append order was not preserved: %s, %s", got[0].GetId(), got[1].GetId())
	}
	// before 상태가 왕복해야 한다 — 되돌림의 근거는 이 값뿐이다.
	if b := got[0].GetBefore(); b == nil || len(b.GetModules()) != 1 ||
		b.GetModules()[0] != "libcrypto.so.3@3.0.13" {
		t.Errorf("the before state did not round-trip: %+v", got[0].GetBefore())
	}
	if got[0].GetStatus() != provisioningv1.ProvisioningStatus_PROVISIONING_STATUS_STAGED {
		t.Errorf("the initial state must be STAGED: %v", got[0].GetStatus())
	}
	if len(got[0].GetAppKeys()) != 1 || got[0].GetAppKeys()[0] != "pay.service" {
		t.Errorf("app_keys spanning several apps was not preserved: %v", got[0].GetAppKeys())
	}
}

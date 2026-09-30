package provisioning_test

import (
	"testing"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// RecordStore — append-only. 노드별 조회로 롤백 근거를 되찾는다.
func TestMemRecordStore(t *testing.T) {
	s := provisioning.NewMemRecordStore()
	_ = s.Append(&provisioningv1.ProvisioningRecord{Id: "r1", NodeId: "web-01", AppKeys: []string{"pay.service"}})
	_ = s.Append(&provisioningv1.ProvisioningRecord{Id: "r2", NodeId: "web-01", AppKeys: []string{"api.service"}})
	_ = s.Append(&provisioningv1.ProvisioningRecord{Id: "r3", NodeId: "db-01"})

	web, _ := s.ByNode("web-01")
	if len(web) != 2 {
		t.Fatalf("%d web-01 records (want 2)", len(web))
	}
	if web[0].GetId() != "r1" || web[1].GetId() != "r2" {
		t.Errorf("the append order was not preserved: %s,%s", web[0].GetId(), web[1].GetId())
	}
	if db, _ := s.ByNode("db-01"); len(db) != 1 {
		t.Errorf("%d db-01 records (want 1)", len(db))
	}
}

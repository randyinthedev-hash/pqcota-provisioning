package provisioning

import (
	"sync"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
)

// RecordStore — 프로비저닝 레코드(before 상태·롤백 근거) 저장소. **append-only**다(§1.3 원본 불변):
// 조치 이력은 갱신·삭제하지 않는다. meta(upsert)와 대비. 상태 전이는 새 레코드가 아니라 별도 추적.
type RecordStore interface {
	Append(*provisioningv1.ProvisioningRecord) error
	ByNode(nodeID string) ([]*provisioningv1.ProvisioningRecord, error)
	All() ([]*provisioningv1.ProvisioningRecord, error)
}

// MemRecordStore — 인메모리 append-only(테스트·단일 실행).
type MemRecordStore struct {
	mu   sync.RWMutex
	recs []*provisioningv1.ProvisioningRecord
}

func NewMemRecordStore() *MemRecordStore { return &MemRecordStore{} }

func (m *MemRecordStore) Append(r *provisioningv1.ProvisioningRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs = append(m.recs, r)
	return nil
}

func (m *MemRecordStore) ByNode(nodeID string) ([]*provisioningv1.ProvisioningRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []*provisioningv1.ProvisioningRecord
	for _, r := range m.recs {
		if r.GetNodeId() == nodeID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemRecordStore) All() ([]*provisioningv1.ProvisioningRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*provisioningv1.ProvisioningRecord, len(m.recs))
	copy(out, m.recs)
	return out, nil
}

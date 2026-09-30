package provisioning

import (
	"context"
	"strings"

	provisioningv1 "github.com/randyinthedev-hash/pqcota-common/gen/pqcota/provisioning/v1"
	"github.com/randyinthedev-hash/pqcota-common/pkg/org"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
)

const recordSchemaSQL = `
CREATE TABLE IF NOT EXISTS pqcota_provisioning_record (
    seq        BIGSERIAL PRIMARY KEY,
    id         TEXT NOT NULL,
    node_id    TEXT NOT NULL,
    app_keys   TEXT,
    plan_id    TEXT,
    record     JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_pqcota_prov_node ON pqcota_provisioning_record(node_id, seq);

-- 조직 축(v0.2.0). 기존 행은 org.Default로. 이행 후 기본값 제거는 docs/compatibility.md §5.
ALTER TABLE pqcota_provisioning_record ADD COLUMN IF NOT EXISTS org TEXT NOT NULL DEFAULT 'default';
CREATE INDEX IF NOT EXISTS idx_pqcota_prov_org ON pqcota_provisioning_record(org, node_id, seq);
`

// PgRecordStore — Postgres append-only 프로비저닝 레코드(§1.2 불변·프로비저닝 설계 §6A 롤백 근거 영속).
//
// **핸들이 조직에 묶인다** — 히스토리·메타 저장소와 같은 규칙이다.
type PgRecordStore struct {
	pool *pgxpool.Pool
	org  org.ID
}

// NewPgRecordStore — 조직을 대지 않고 연다(org.Default). 시그니처를 바꾸지 않는다.
func NewPgRecordStore(ctx context.Context, dsn string) (*PgRecordStore, error) {
	return NewPgRecordStoreIn(ctx, dsn, "")
}

// NewPgRecordStoreIn — 조직에 묶인 레코드 저장소.
func NewPgRecordStoreIn(ctx context.Context, dsn, organization string) (*PgRecordStore, error) {
	o, err := org.Resolve(organization)
	if err != nil {
		return nil, err
	}
	return newPgRecordStore(ctx, dsn, o)
}

func newPgRecordStore(ctx context.Context, dsn string, o org.ID) (*PgRecordStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if _, err := pool.Exec(ctx, recordSchemaSQL); err != nil {
		pool.Close()
		return nil, err
	}
	return &PgRecordStore{pool: pool, org: o}, nil
}

// Org — 이 핸들이 묶인 조직(org.Scoped).
func (p *PgRecordStore) Org() org.ID { return p.org }

func (p *PgRecordStore) Close() { p.pool.Close() }

func (p *PgRecordStore) Append(r *provisioningv1.ProvisioningRecord) error {
	j, err := protojson.Marshal(r)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(context.Background(),
		`INSERT INTO pqcota_provisioning_record(org,id,node_id,app_keys,plan_id,record) VALUES($1,$2,$3,$4,$5,$6)`,
		p.org,
		r.GetId(), r.GetNodeId(), strings.Join(r.GetAppKeys(), ","), r.GetPlanId(), j)
	return err
}

func (p *PgRecordStore) All() ([]*provisioningv1.ProvisioningRecord, error) {
	return p.query(`SELECT record FROM pqcota_provisioning_record WHERE org=$1 ORDER BY seq ASC`, p.org)
}

func (p *PgRecordStore) ByNode(nodeID string) ([]*provisioningv1.ProvisioningRecord, error) {
	return p.query(`SELECT record FROM pqcota_provisioning_record WHERE org=$1 AND node_id=$2 ORDER BY seq ASC`, p.org, nodeID)
}

func (p *PgRecordStore) query(sql string, args ...any) ([]*provisioningv1.ProvisioningRecord, error) {
	rows, err := p.pool.Query(context.Background(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*provisioningv1.ProvisioningRecord
	for rows.Next() {
		var j []byte
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		r := &provisioningv1.ProvisioningRecord{}
		if err := protojson.Unmarshal(j, r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

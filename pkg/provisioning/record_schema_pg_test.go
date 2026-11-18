// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package provisioning_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/history"
	"github.com/randyinthedev-hash/pqcota-inventory/pkg/inventory/pgschema"
	"github.com/randyinthedev-hash/pqcota-provisioning/pkg/provisioning"
)

// 조회용 열기는 빈 DB에 표를 만들지 않고, PQCOTA_AUTO_DDL=0인 쓰기 열기도 만들지 않는다.
// 초기화(-init-schema가 부르는 것)가 표를 만들고, 그 뒤에는 둘 다 열린다. 설정하지 않은 쓰기 열기는 지금처럼 만든다.
func TestRecordSchemaInitAndOpen(t *testing.T) {
	dsn := os.Getenv("PQCOTA_TEST_DSN")
	if dsn == "" {
		t.Skip("PQCOTA_TEST_DSN is not set — skipping the Postgres integration test")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "pqcota_rec_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	scoped := dsn + sep + "search_path=" + schema
	tables := func() int {
		var n int
		if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname=$1`, schema).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if _, err := provisioning.OpenPgRecordStoreIn(ctx, scoped, "acme"); !errors.Is(err, history.ErrSchemaMissing) ||
		!strings.Contains(err.Error(), "pqcota-records -init-schema") {
		t.Fatalf("read-only open on an empty schema: %v", err)
	}
	t.Setenv(history.AutoDDLEnv, "0")
	if _, err := provisioning.NewPgRecordStoreIn(ctx, scoped, "acme"); !errors.Is(err, history.ErrSchemaMissing) {
		t.Fatalf("AUTO_DDL=0 write open on an empty schema: %v", err)
	}
	if n := tables(); n != 0 {
		t.Fatalf("opening created %d tables", n)
	}

	pool, err := pgxpool.New(ctx, scoped)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rep, err := pgschema.Init(ctx, pool, provisioning.RecordSchema())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rep.Created, []string{"pqcota_provisioning_record"}) {
		t.Fatalf("init: %+v", rep)
	}
	if r, err := provisioning.OpenPgRecordStoreIn(ctx, scoped, "acme"); err != nil {
		t.Fatal(err)
	} else {
		r.Close()
	}
	if w, err := provisioning.NewPgRecordStoreIn(ctx, scoped, "acme"); err != nil {
		t.Fatal(err)
	} else {
		w.Close()
	}

	// 설정하지 않으면 쓰기 열기가 지금처럼 표를 만든다.
	if _, err := admin.Exec(ctx, "DROP TABLE "+schema+".pqcota_provisioning_record"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(history.AutoDDLEnv, "")
	if w, err := provisioning.NewPgRecordStoreIn(ctx, scoped, "acme"); err != nil {
		t.Fatal(err)
	} else {
		w.Close()
	}
	if n := tables(); n != 1 {
		t.Fatalf("automatic DDL: %d tables", n)
	}
}

// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// 시험 실행 파일이 자기 자신을 이 명령으로 다시 띄운다.
func TestMain(m *testing.M) {
	if os.Getenv("PQCOTA_RECORDS_TEST_MAIN") == "1" {
		os.Args = append([]string{"pqcota-records"}, strings.Fields(os.Getenv("PQCOTA_RECORDS_TEST_ARGS"))...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runRecords(t *testing.T, dsn string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		"PQCOTA_RECORDS_TEST_MAIN=1",
		"PQCOTA_RECORDS_TEST_ARGS="+strings.Join(args, " "),
		"PQCOTA_DSN="+dsn,
	)
	var o, e strings.Builder
	cmd.Stdout, cmd.Stderr = &o, &e
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), o.String(), e.String()
	} else if err != nil {
		t.Fatal(err)
	}
	return 0, o.String(), e.String()
}

// -init-schema는 DSN이 없거나 노드 인자와 함께 오면 접속하기 전에 종료 2로 거부한다.
func TestInitSchemaRefusesOtherArguments(t *testing.T) {
	unreachable := "postgres://nobody@127.0.0.1:1/none?connect_timeout=1"
	for name, c := range map[string]struct {
		dsn  string
		args []string
	}{
		"no DSN":     {"", []string{"-init-schema"}},
		"a node":     {unreachable, []string{"-init-schema", "web-01"}},
		"two nodes":  {unreachable, []string{"web-01", "web-02"}},
		"node first": {unreachable, []string{"web-01", "-init-schema"}},
	} {
		code, out, errOut := runRecords(t, c.dsn, c.args...)
		if code != 2 {
			t.Errorf("%s: exit %d, want 2 (stderr %q)", name, code, errOut)
		}
		if strings.Contains(out, "schema ready") {
			t.Errorf("%s: printed a success line", name)
		}
	}
}

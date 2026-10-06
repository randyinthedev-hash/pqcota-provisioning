// SPDX-FileCopyrightText: 2026 Great Honor <randyinthedev@gmail.com>
// SPDX-License-Identifier: Apache-2.0

package main

// TP-GATE-20 — 사용법이 틀리면 서명하지 않고 알려 준다: `--approver`·계획 파일·`PQCOTA_APPROVAL_KEY`가
// 없으면 종료 2, 승인자 id에 `:`가 있으면 서명 단계에서 거절(종료 1). 어느 경우에도 stdout은 비어 있다.
//
// `:` 규칙 자체는 pqcota-common의 sign 시험이 보고, 여기서는 **명령이 그 거절을 종료 상태와 빈 stdout으로
// 옮기는가**를 본다. 서명 대상 계획을 내버리면 받아 가는 사람이 생긴다.

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/randyinthedev-hash/pqcota-common/pkg/kernel/sign"
)

func TestUsageErrors(t *testing.T) {
	_, priv, _ := sign.Generate()
	bin := buildCLI(t)
	plan := writePlan(t, judged)

	for _, tc := range []struct {
		name    string
		args    []string
		key     string // PQCOTA_APPROVAL_KEY. 빈 값이면 환경에서 지운다
		want    int
		wantErr string
	}{
		{"승인자를 주지 않았다", []string{plan}, priv, 2, "usage"},
		{"계획 파일을 주지 않았다", []string{"--approver", "reviewer-1"}, priv, 2, "usage"},
		{"서명할 키가 없다", []string{"--approver", "reviewer-1", plan}, "", 2, "PQCOTA_APPROVAL_KEY is not set"},
		{"승인자 id에 ':'가 있다", []string{"--approver", "a:b", plan}, priv, 1, "signing:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env []string
			for _, e := range os.Environ() {
				if !strings.HasPrefix(e, "PQCOTA_APPROVAL_KEY=") {
					env = append(env, e)
				}
			}
			if tc.key != "" {
				env = append(env, "PQCOTA_APPROVAL_KEY="+tc.key)
			}
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = env
			var so, se strings.Builder
			cmd.Stdout, cmd.Stderr = &so, &se
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != tc.want {
				t.Fatalf("종료 코드가 %d여야 한다(%v):\n%s", tc.want, err, se.String())
			}
			if !strings.Contains(se.String(), tc.wantErr) {
				t.Errorf("stderr에 %q가 없다:\n%s", tc.wantErr, se.String())
			}
			if strings.TrimSpace(so.String()) != "" {
				t.Errorf("서명하지 못했는데 stdout에 계획을 냈다:\n%s", so.String())
			}
		})
	}
}

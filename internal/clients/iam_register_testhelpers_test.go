// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package clients_test

import "testing"

// testLoggerWriter — io.Writer-адаптер, направляющий slog-вывод drainer'а в t.Log,
// чтобы диагностика register-drainer'а появлялась под запущенным тестом (и нигде в
// проде). Используется newRegisterDrainer (iam_register_drainer_integration_test.go).
type testLoggerWriter struct{ t *testing.T }

func (w testLoggerWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package main

import (
	"errors"
	"fmt"

	"github.com/PRO-Robotech/kacho-corelib/authz"

	"github.com/PRO-Robotech/kacho-vpc/internal/apps/kacho/check"
)

// fatalAuthzInterceptorAbsent — frozen-текст fail-fast'а S3: production-инстанс
// не стартует без authz-interceptor'а (защита от регрессии, обходящей S1-гард).
const fatalAuthzInterceptorAbsent = "production mode requires authz interceptor but " +
	"kacho-iam connection is not configured (authz.iam-endpoint unset and authz.breakglass=false)"

// authzWiringDecision решает судьбу authz-interceptor'а по результату
// check.NewInterceptor и режиму работы (defense-in-depth, S3):
//
//   - interceptor собран (err==nil) → возвращается для навешивания на обе цепочки;
//   - ErrIAMConnNotConfigured в production → ФАТАЛЬНАЯ ошибка (процесс не стартует
//     без authz-Check — иначе подделанная x-kacho-* metadata дает эскалацию);
//   - ErrIAMConnNotConfigured в dev → (nil, nil): caller логирует WARN и
//     продолжает без authz-interceptor'а (dev backward-compat);
//   - прочая build-ошибка → пробрасывается как есть.
func authzWiringDecision(productionMode bool, intr *authz.Interceptor, err error) (*authz.Interceptor, error) {
	switch {
	case err == nil:
		return intr, nil
	case errors.Is(err, check.ErrIAMConnNotConfigured):
		if productionMode {
			return nil, fmt.Errorf("%s", fatalAuthzInterceptorAbsent)
		}
		return nil, nil
	default:
		return nil, err
	}
}

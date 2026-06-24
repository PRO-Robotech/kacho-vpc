// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package handler

import "github.com/PRO-Robotech/kacho-vpc/internal/repo/repomock"

// newMockOpsRepo — shim над repomock.NewOpsRepo.
var newMockOpsRepo = repomock.NewOpsRepo

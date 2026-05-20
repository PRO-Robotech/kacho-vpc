// Package fgawrite — write-side OpenFGA integration for kacho-vpc (KAC-127
// issue #22).
//
// kacho-vpc already has a read-side FGA path (`listauthz` ListObjects filter,
// Phase 4) and a per-RPC Check interceptor, but it never published the
// per-resource hierarchy tuple a created resource needs:
//
//	vpc_network:<id>          #project @project:<project_id>
//	vpc_subnet:<id>           #project @project:<project_id>
//	vpc_security_group:<id>   #project @project:<project_id>
//	vpc_route_table:<id>      #project @project:<project_id>
//	vpc_address:<id>          #project @project:<project_id>
//	vpc_gateway:<id>          #project @project:<project_id>
//	vpc_private_endpoint:<id> #project @project:<project_id>
//
// Every vpc_* FGA type carries a `project: [project]` parent relation with the
// admin/editor/viewer cascade `<rel> from project`. Without the parent-pointer
// tuple a per-resource Get/Update/Delete Check has no path to the project where
// the principal's role binding lives → fail-closed DENY.
//
// The writer is invoked from each resource's Operation worker AFTER the
// resource row is committed. It is best-effort + non-fatal: the row is already
// durable, a tuple-write failure is logged for the operator (parity with the
// kacho-iam fgahook). The HTTP client itself retries transient failures.
package fgawrite

import (
	"context"
	"fmt"
	"log/slog"
)

// HierarchyTupleWriter — port-interface a vpc Create use-case needs to publish
// the resource→project hierarchy tuple. Implemented by
// internal/clients.OpenFGAWriteClient (composition root wires it; nil when
// OpenFGA tuple-write is not configured).
type HierarchyTupleWriter interface {
	// WriteHierarchyTuple writes `<objectType>:<objectID>#project@project:<projectID>`.
	// Idempotent — re-writing an existing tuple is a no-op success.
	WriteHierarchyTuple(ctx context.Context, objectType, objectID, projectID string) error
}

// Emit publishes the resource→project hierarchy tuple, best-effort. A nil
// writer is a no-op (OpenFGA tuple-write not configured — dev / degraded mode).
// Failures are logged, never returned — the resource row is already committed
// and an Operation must not fail because of a downstream FGA hiccup.
//
// objectType is the vpc_* FGA type ("vpc_network", "vpc_subnet", ...).
func Emit(ctx context.Context, w HierarchyTupleWriter, logger *slog.Logger, objectType, objectID, projectID string) {
	if w == nil {
		return
	}
	if objectID == "" || projectID == "" {
		if logger != nil {
			logger.Warn("vpc fga hierarchy-tuple skipped: empty id (KAC-127 #22)",
				"object_type", objectType, "object_id", objectID, "project_id", projectID)
		}
		return
	}
	if err := w.WriteHierarchyTuple(ctx, objectType, objectID, projectID); err != nil {
		if logger != nil {
			logger.Warn("vpc fga hierarchy-tuple write failed (KAC-127 #22)",
				"err", err, "object", fmt.Sprintf("%s:%s", objectType, objectID),
				"project", projectID)
		}
		return
	}
	if logger != nil {
		logger.Info("vpc fga hierarchy-tuple written (KAC-127 #22)",
			"object", fmt.Sprintf("%s:%s", objectType, objectID), "project", projectID)
	}
}

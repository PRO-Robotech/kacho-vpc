package service

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// timestampProto конвертирует time.Time в *timestamppb.Timestamp.
func timestampProto(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}

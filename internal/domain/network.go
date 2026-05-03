package domain

import "time"

// Network — виртуальная сеть Kachō VPC.
type Network struct {
	UID               string
	FolderID          string
	CloudID           string
	OrganizationID    string
	Name              string
	Labels            map[string]string
	Annotations       map[string]string
	CreationTimestamp time.Time
	ResourceVersion   int64
	Generation        int64
	DeletionTimestamp *time.Time
	Finalizers        []string
	DisplayName       string
	Description       string
	State             string
}

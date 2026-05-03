package domain

import "time"

// StaticRoute — статический маршрут внутри RouteTable.
type StaticRoute struct {
	ID                  string
	DestinationPrefix   string
	NextHopAddress      string
	Description         string
}

// RouteTable — таблица маршрутизации, привязана к Network.
type RouteTable struct {
	UID               string
	NetworkID         string
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
	StaticRoutes      []StaticRoute
	State             string
}

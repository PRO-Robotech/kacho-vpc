package domain

import "time"

// StaticRoute — статический маршрут.
type StaticRoute struct {
	DestinationPrefix string            `json:"destination_prefix"`
	NextHopAddress    string            `json:"next_hop_address"`
	Labels            map[string]string `json:"labels,omitempty"`
}

// RouteTable — таблица маршрутизации.
type RouteTable struct {
	ID           string
	FolderID     string
	CreatedAt    time.Time
	Name         string
	Description  string
	Labels       map[string]string
	NetworkID    string
	StaticRoutes []StaticRoute
}

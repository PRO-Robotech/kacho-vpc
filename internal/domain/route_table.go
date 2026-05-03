package domain

import "time"

// RouteTableStatus — статус таблицы маршрутизации.
type RouteTableStatus int

const (
	RouteTableStatusUnspecified  RouteTableStatus = 0
	RouteTableStatusProvisioning RouteTableStatus = 1
	RouteTableStatusActive       RouteTableStatus = 2
	RouteTableStatusDeleting     RouteTableStatus = 3
)

// RouteTableStatusString — строковые значения статусов таблицы маршрутизации.
var RouteTableStatusString = map[RouteTableStatus]string{
	RouteTableStatusUnspecified:  "ROUTE_TABLE_STATUS_UNSPECIFIED",
	RouteTableStatusProvisioning: "ROUTE_TABLE_STATUS_PROVISIONING",
	RouteTableStatusActive:       "ROUTE_TABLE_STATUS_ACTIVE",
	RouteTableStatusDeleting:     "ROUTE_TABLE_STATUS_DELETING",
}

// ParseRouteTableStatus парсит строку статуса.
func ParseRouteTableStatus(s string) RouteTableStatus {
	for k, v := range RouteTableStatusString {
		if v == s {
			return k
		}
	}
	return RouteTableStatusUnspecified
}

// StaticRoute — статический маршрут.
type StaticRoute struct {
	// ID server-assigned UUID маршрута.
	ID                string
	DestinationPrefix string
	NextHopAddress    string
	Description       string
}

// RouteTable — таблица маршрутизации VPC (sub-phase 1.0).
// static_routes[] хранятся как JSONB внутри строки; Update — full-replace.
type RouteTable struct {
	ID                     string
	FolderID               string
	NetworkID              string
	Name                   string
	Description            string
	CreatedAt              time.Time
	Labels                 map[string]string
	Status                 RouteTableStatus
	Generation             int64
	ResourceVersion        string
	StaticRoutes           []StaticRoute
	DeletedAt              *time.Time
}

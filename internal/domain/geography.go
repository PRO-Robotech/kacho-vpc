package domain

import "time"

// Region — глобальный географический ресурс. PrimaryKey: id (e.g. "ru-central1").
type Region struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Zone — зона внутри Region. PrimaryKey: id (e.g. "ru-central1-a").
// FK на Region (ON DELETE RESTRICT) — нельзя удалить Region с зонами.
type Zone struct {
	ID        string
	RegionID  string
	Name      string
	CreatedAt time.Time
}

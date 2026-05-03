package service

import "errors"

// ErrNotFound возвращается, когда ресурс не найден.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists возвращается при нарушении UNIQUE constraint.
var ErrAlreadyExists = errors.New("already exists")

// ErrInvalidArg возвращается при некорректных входных данных.
var ErrInvalidArg = errors.New("invalid argument")

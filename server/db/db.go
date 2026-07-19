package db

import (
	"errors"

	"mutagen-web/server/models"
	"mutagen-web/server/store"
)

var S *store.Store

func Init(dataPath string) error {
	var err error
	S, err = store.New(dataPath)
	return err
}

func GetStore() *store.Store {
	if S == nil {
		panic("store not initialized")
	}
	return S
}

func IsNotFound(err error) bool {
	return errors.Is(err, ErrRecordNotFound)
}

var ErrRecordNotFound = errors.New("record not found")

// Helper functions for compatibility with old gorm-style code
func Create(value interface{}) error {
	switch v := value.(type) {
	case *models.Machine:
		return S.CreateMachine(v)
	case *models.SyncTask:
		return S.CreateTask(v)
	}
	return errors.New("unsupported type")
}

func Save(value interface{}) error {
	switch v := value.(type) {
	case *models.Machine:
		return S.SaveMachine(v)
	case *models.SyncTask:
		return S.SaveTask(v)
	}
	return errors.New("unsupported type")
}

func Delete(value interface{}, id uint) error {
	switch value.(type) {
	case *models.Machine:
		return S.DeleteMachine(id)
	case *models.SyncTask:
		return S.DeleteTask(0, id)
	}
	return errors.New("unsupported type")
}

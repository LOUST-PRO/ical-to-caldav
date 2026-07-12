package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/LOUST-PRO/ical-to-caldav/internal/source"
)

const (
	sourcePrefixStr = "src"
	eventPrefixStr = "evt"
	lastFetchPrefix = "lmf"
)

type BadgerStore struct {
	db *badger.DB
}

func NewBadgerStore(dataDir string) (*BadgerStore, error) {
	dir := expandTilde(dataDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	db, err := badger.Open(badger.DefaultOptions(dir))
	if err != nil {
		return nil, fmt.Errorf("open badger %s: %w", dir, err)
	}
	return &BadgerStore{db: db}, nil
}

func bkey(s string) []byte { return []byte(s) }

func (s *BadgerStore) SaveSource(sourceID string, events []*source.Event) error {
	srcKey := bkey(sourcePrefixStr + ":" + sourceID)
	return s.db.Update(func(txn *badger.Txn) error {
		// Delete old events
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		prefix := eventPrefixStr + ":" + sourceID + ":"
		for it.Seek(bkey(prefix)); it.ValidForPrefix(bkey(prefix)); it.Next() {
			if err := txn.Delete(it.Item().Key()); err != nil {
				return err
			}
		}
		it.Close()

		// Save source marker
		if err := txn.Set(srcKey, []byte(sourceID)); err != nil {
			return err
		}

		// Save events
		for _, ev := range events {
			data, err := json.Marshal(ev)
			if err != nil {
				return err
			}
			ek := bkey(eventPrefixStr + ":" + sourceID + ":" + ev.ID)
			if err := txn.Set(ek, data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *BadgerStore) ListEvents(sourceID string, from, to *time.Time) ([]*source.Event, error) {
	var out []*source.Event
	prefix := eventPrefixStr + ":" + sourceID + ":"
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(bkey(prefix)); it.ValidForPrefix(bkey(prefix)); it.Next() {
			var ev source.Event
			item := it.Item()
			if err := item.Value(func(v []byte) error {
				return json.Unmarshal(v, &ev)
			}); err != nil {
				continue
			}
			if from != nil && ev.End.Before(*from) {
				continue
			}
			if to != nil && ev.Start.After(*to) {
				continue
			}
			out = append(out, &ev)
		}
		return nil
	})
	return out, err
}

func (s *BadgerStore) ListSources() ([]string, error) {
	var ids []string
	prefix := sourcePrefixStr + ":"
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		seen := make(map[string]bool)
		for it.Seek(bkey(prefix)); it.ValidForPrefix(bkey(prefix)); it.Next() {
			key := string(it.Item().Key())
			id := strings.TrimPrefix(key, prefix)
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		return nil
	})
	return ids, err
}

func (s *BadgerStore) LastFetch(sourceID string) (time.Time, error) {
	var t time.Time
	key := bkey(lastFetchPrefix + ":" + sourceID)
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err == badger.ErrKeyNotFound {
			return nil
		}
		if err != nil {
			return err
		}
		return item.Value(func(v []byte) error {
			return json.Unmarshal(v, &t)
		})
	})
	return t, err
}

func (s *BadgerStore) SetLastFetch(sourceID string, t time.Time) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(bkey(lastFetchPrefix+":"+sourceID), data)
	})
}

func (s *BadgerStore) Close() error {
	return s.db.Close()
}

func expandTilde(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, _ := os.UserHomeDir(); home != "" {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

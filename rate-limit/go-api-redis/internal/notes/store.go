package notes

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrNotFound = errors.New("note not found")

type Note struct {
	ID        string    `json:"id"`
	OwnerID   string    `json:"owner_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	client *redis.Client
}

func NewStore(client *redis.Client) *Store {
	return &Store{client: client}
}

func noteKey(id string) string      { return "note:" + id }
func noteIndexKey(id string) string { return "user:" + id + ":notes" }
func timestamp(t time.Time) string  { return t.UTC().Format(time.RFC3339Nano) }
func parseTime(v string) time.Time  { parsed, _ := time.Parse(time.RFC3339Nano, v); return parsed }
func (n Note) fields() map[string]any {
	return map[string]any{
		"id":         n.ID,
		"owner_id":   n.OwnerID,
		"title":      n.Title,
		"body":       n.Body,
		"created_at": timestamp(n.CreatedAt),
		"updated_at": timestamp(n.UpdatedAt),
	}
}

func (s *Store) Create(ctx context.Context, ownerID, title, body string) (Note, error) {
	now := time.Now().UTC()
	note := Note{
		ID:        uuid.NewString(),
		OwnerID:   ownerID,
		Title:     title,
		Body:      body,
		CreatedAt: now,
		UpdatedAt: now,
	}

	pipe := s.client.TxPipeline()
	pipe.HSet(ctx, noteKey(note.ID), note.fields())
	pipe.ZAdd(ctx, noteIndexKey(ownerID), redis.Z{
		Score:  float64(now.UnixMilli()),
		Member: note.ID,
	})
	if _, err := pipe.Exec(ctx); err != nil {
		return Note{}, err
	}

	return note, nil
}

func (s *Store) Get(ctx context.Context, ownerID, id string) (Note, error) {
	fields, err := s.client.HGetAll(ctx, noteKey(id)).Result()
	if err != nil {
		return Note{}, err
	}
	if len(fields) == 0 || fields["owner_id"] != ownerID {
		return Note{}, ErrNotFound
	}

	return Note{
		ID:        fields["id"],
		OwnerID:   fields["owner_id"],
		Title:     fields["title"],
		Body:      fields["body"],
		CreatedAt: parseTime(fields["created_at"]),
		UpdatedAt: parseTime(fields["updated_at"]),
	}, nil
}

func (s *Store) List(ctx context.Context, ownerID string, offset, limit int64) ([]Note, int64, error) {
	total, err := s.client.ZCard(ctx, noteIndexKey(ownerID)).Result()
	if err != nil {
		return nil, 0, err
	}

	ids, err := s.client.ZRevRange(ctx, noteIndexKey(ownerID), offset, offset+limit-1).Result()
	if err != nil {
		return nil, 0, err
	}

	items := make([]Note, 0, len(ids))
	for _, id := range ids {
		note, err := s.Get(ctx, ownerID, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		items = append(items, note)
	}

	return items, total, nil
}

func (s *Store) Update(ctx context.Context, ownerID, id string, title, body *string) (Note, error) {
	note, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return Note{}, err
	}

	if title != nil {
		note.Title = *title
	}
	if body != nil {
		note.Body = *body
	}
	note.UpdatedAt = time.Now().UTC()

	if err := s.client.HSet(ctx, noteKey(id), note.fields()).Err(); err != nil {
		return Note{}, err
	}
	return note, nil
}

func (s *Store) Delete(ctx context.Context, ownerID, id string) error {
	if _, err := s.Get(ctx, ownerID, id); err != nil {
		return err
	}

	pipe := s.client.TxPipeline()
	pipe.Del(ctx, noteKey(id))
	pipe.ZRem(ctx, noteIndexKey(ownerID), id)
	_, err := pipe.Exec(ctx)
	return err
}

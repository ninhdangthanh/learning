package store

import (
	"errors"
	"testing"
)

func TestCreateThenGet(t *testing.T) {
	s := New()

	created := s.Create("Tai nghe Sony WH-1000XM5", 7990000, 8)

	got, err := s.Get(created.ID)
	if err != nil {
		t.Fatalf("Get(%q) trả về lỗi: %v", created.ID, err)
	}
	if got != created {
		t.Fatalf("Get(%q) = %+v, muốn %+v", created.ID, got, created)
	}
}

func TestListIsSortedByID(t *testing.T) {
	s := New()

	list := s.List()

	if len(list) != 3 {
		t.Fatalf("len(List()) = %d, muốn 3", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID >= list[i].ID {
			t.Fatalf("List() không sắp xếp theo ID: %q đứng trước %q", list[i-1].ID, list[i].ID)
		}
	}
}

func TestUpdateMissingReturnsErrNotFound(t *testing.T) {
	s := New()

	_, err := s.Update("khong-ton-tai", "x", 1, 1)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(id lạ) = %v, muốn ErrNotFound", err)
	}
}

func TestDeleteRemovesProduct(t *testing.T) {
	s := New()
	created := s.Create("Dock Anker 555", 1890000, 4)

	if err := s.Delete(created.ID); err != nil {
		t.Fatalf("Delete(%q) trả về lỗi: %v", created.ID, err)
	}

	if _, err := s.Get(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get sau Delete = %v, muốn ErrNotFound", err)
	}
}

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"lock-lab/internal/db"
)

const (
	tableName  = "lock_test_orders"
	columnName = "token"
)

func main() {
	lockTimeout := os.Getenv("LOCK_TIMEOUT")
	if lockTimeout == "" {
		lockTimeout = "10s"
	}

	ctx := context.Background()
	conn, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("connect thất bại: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, fmt.Sprintf("SET lock_timeout = '%s'", lockTimeout)); err != nil {
		log.Fatalf("set lock_timeout thất bại: %v", err)
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN IF EXISTS %s", tableName, columnName)); err != nil {
		log.Fatalf("drop column thất bại: %v", err)
	}

	before, err := db.InspectTable(ctx, conn, tableName)
	if err != nil {
		log.Fatalf("inspect bảng thất bại: %v", err)
	}
	log.Printf("Trước: filenode=%d, size=%s", before.Filenode, before.Size)

	log.Printf("ALTER TABLE ADD COLUMN NOT NULL DEFAULT gen_random_uuid()... (lock_timeout=%s)", lockTimeout)
	startedAt := time.Now()

	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s uuid NOT NULL DEFAULT gen_random_uuid()", tableName, columnName)
	if _, err := conn.Exec(ctx, stmt); err != nil {
		log.Fatalf("thất bại sau %.1fs: %v", time.Since(startedAt).Seconds(), err)
	}
	elapsed := time.Since(startedAt).Seconds()

	after, err := db.InspectTable(ctx, conn, tableName)
	if err != nil {
		log.Fatalf("inspect bảng thất bại: %v", err)
	}
	log.Printf("Sau:   filenode=%d, size=%s", after.Filenode, after.Size)

	if after.Filenode != before.Filenode {
		log.Printf("Filenode ĐỔI (%d -> %d): PostgreSQL đã REWRITE toàn bảng.", before.Filenode, after.Filenode)
	} else {
		log.Printf("Filenode không đổi: metadata-only, không rewrite.")
	}

	log.Printf("Xong sau %.1fs.", elapsed)
	log.Printf("Lưu ý: lock_timeout=%s chỉ giới hạn thời gian CHỜ lấy lock, không giới hạn", lockTimeout)
	log.Println("thời gian GIỮ lock. Rewrite vẫn chạy tới cùng — muốn chặn phải dùng statement_timeout.")
}

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
	columnName = "is_priority"
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

	prep := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s boolean NOT NULL DEFAULT false", tableName, columnName)
	if _, err := conn.Exec(ctx, prep); err != nil {
		log.Fatalf("chuẩn bị cột thất bại: %v", err)
	}

	before, err := db.InspectTable(ctx, conn, tableName)
	if err != nil {
		log.Fatalf("inspect bảng thất bại: %v", err)
	}
	log.Printf("Trước: filenode=%d, size=%s", before.Filenode, before.Size)

	log.Printf("ALTER TABLE DROP COLUMN %s... (lock_timeout=%s)", columnName, lockTimeout)
	startedAt := time.Now()

	stmt := fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", tableName, columnName)
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
		log.Printf("Filenode không đổi: chỉ đánh dấu attisdropped trong pg_attribute.")
	}

	log.Printf("Xong sau %.1fs.", elapsed)
	log.Println("Size không giảm: data cũ vẫn nằm trong từng tuple, phải VACUUM FULL")
	log.Println("hoặc pg_repack mới lấy lại được disk space.")
}

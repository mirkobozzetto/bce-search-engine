package csv

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lib/pq"
)

func ProcessCSVOptimized(db *sql.DB, csvPath, tableName string) error {
	start := time.Now()

	file, err := os.Open(csvPath)
	if err != nil {
		return fmt.Errorf("impossible to open %s: %v", csvPath, err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comma = ','

	headers, err := reader.Read()
	if err != nil {
		return fmt.Errorf("error reading header: %v", err)
	}

	fmt.Printf("📄 CSV: %s\n", filepath.Base(csvPath))
	fmt.Printf("📊 Columns: %v\n", headers)

	// Optimize PostgreSQL settings for bulk insert
	optimizeForBulkInsert(db)

	// Drop table
	dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)
	if _, err := db.Exec(dropSQL); err != nil {
		return fmt.Errorf("error dropping table: %v", err)
	}

	// Create table with optimizations
	var columns []string
	cleanHeaders := make([]string, len(headers))
	for i, header := range headers {
		cleanHeader := strings.ReplaceAll(header, " ", "_")
		cleanHeader = strings.ReplaceAll(cleanHeader, "-", "_")
		cleanHeader = strings.ToLower(cleanHeader)
		cleanHeaders[i] = cleanHeader
		columns = append(columns, cleanHeader+" TEXT")
	}

	createSQL := fmt.Sprintf("CREATE UNLOGGED TABLE %s (%s)", tableName, strings.Join(columns, ", "))
	fmt.Printf("🏗️ Creating UNLOGGED table: %s\n", tableName)

	if _, err := db.Exec(createSQL); err != nil {
		return fmt.Errorf("error creating table: %v", err)
	}

	// Use COPY for maximum speed
	lineCount, err := copyInsert(db, reader, tableName, cleanHeaders)
	if err != nil {
		return err
	}

	elapsed := time.Since(start)
	linesPerSec := float64(lineCount) / elapsed.Seconds()

	fmt.Printf("🚀 COPY: %d lines in %.2f sec (%.0f lines/sec)\n",
		lineCount, elapsed.Seconds(), linesPerSec)
	return nil
}

func copyInsert(db *sql.DB, reader *csv.Reader, tableName string, headers []string) (int, error) {
	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("error starting transaction: %v", err)
	}
	defer tx.Rollback()

	// Prepare COPY statement
	stmt, err := tx.Prepare(pq.CopyIn(tableName, headers...))
	if err != nil {
		return 0, fmt.Errorf("error preparing COPY: %v", err)
	}

	lineCount := 0
	startTime := time.Now()

	fmt.Println("🔥 Starting COPY stream...")

	// Stream data directly to PostgreSQL
	for {
		record, err := reader.Read()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return 0, fmt.Errorf("error reading line %d: %v", lineCount+1, err)
		}

		// Convert to interface{} slice
		values := make([]interface{}, len(record))
		for i, v := range record {
			values[i] = v
		}

		if _, err := stmt.Exec(values...); err != nil {
			return 0, fmt.Errorf("error copying line %d: %v", lineCount+1, err)
		}

		lineCount++

		// Progress every 100k lines
		if lineCount%100000 == 0 {
			elapsed := time.Since(startTime)
			linesPerSec := float64(lineCount) / elapsed.Seconds()
			fmt.Printf("🔥 COPY: %d lines (%.0f lines/sec)\n", lineCount, linesPerSec)
		}
	}

	// Finalize COPY
	if _, err := stmt.Exec(); err != nil {
		return 0, fmt.Errorf("error finalizing COPY: %v", err)
	}

	if err := stmt.Close(); err != nil {
		return 0, fmt.Errorf("error closing COPY: %v", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("error committing transaction: %v", err)
	}

	return lineCount, nil
}

func optimizeForBulkInsert(db *sql.DB) error {
	optimizations := []string{
		"SET synchronous_commit = OFF",
		"SET wal_buffers = '128MB'",
		"SET checkpoint_segments = 64",
		"SET checkpoint_completion_target = 0.9",
		"SET maintenance_work_mem = '1GB'",
		"SET work_mem = '512MB'",
		"SET shared_buffers = '512MB'",
		"SET effective_cache_size = '2GB'",
		"SET fsync = OFF", // DANGER: Only for imports!
	}

	for _, sql := range optimizations {
		if _, err := db.Exec(sql); err != nil {
			continue
		}
	}

	fmt.Println("🚀 PostgreSQL optimized for COPY")
	return nil
}

// Keep the old function for compatibility
func ProcessCSV(db *sql.DB, csvPath, tableName string) error {
	return ProcessCSVOptimized(db, csvPath, tableName)
}

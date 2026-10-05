package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lan/meta-gateway/internal/store"
)

// genDatabaseSchema dumps the schema the code actually produces: it migrates a
// throwaway database and reads it back, instead of parsing the SQL files.
//
// Parsing was the obvious approach and the wrong one — the migration set
// contains table-rebuild staging names (`channels_new`), and a parser would
// either report those as real tables or silently drop whatever the rebuild
// added. Migrating and asking SQLite is the only version that cannot drift.
func genDatabaseSchema(root string) (string, error) {
	dir, err := os.MkdirTemp("", "docsgen-schema-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	// store.Open takes a DATA DIRECTORY and creates the database inside it,
	// so pass the directory rather than a file path.
	db, err := store.Open(dir)
	if err != nil {
		return "", fmt.Errorf("migrate a throwaway database: %w", err)
	}
	defer db.Close()

	tables, err := listTables(db)
	if err != nil {
		return "", err
	}
	applied, err := appliedMigrationCount(db)
	if err != nil {
		return "", err
	}
	indexes, err := listIndexes(db)
	if err != nil {
		return "", err
	}
	sqlFiles, err := migrationFileCount(filepath.Join(root, "internal", "store"))
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(heading(1, "数据表与迁移"))
	b.WriteString(paragraph(
		"引擎是 SQLite（WAL 模式），迁移脚本是 `internal/store/NNN_*.sql`，按文件名的数字序执行。",
		"本页由生成器**实际迁移一个临时数据库再读回 schema**得出，所以它反映的是代码最终产生的结构，"+
			"而不是对 SQL 文本的解析结果。",
	))
	b.WriteString(paragraph(
		"当前：`internal/store/` 下 **" + itoa(sqlFiles) + "** 个 `.sql` 文件，" +
			"其中 **" + itoa(applied) + "** 个已应用。两者不等是正常的——迁移历史上存在编号重复与退休：" +
			"`026` / `027` / `028` 各有两个文件（按后缀安全排序），`060` 被 `067` 退休。",
	))

	b.WriteString(heading(2, "迁移铁律"))
	b.WriteString(bullet([]string{
		"**`schema_migrations` 表绝不能删。** 删了会在重启时重放迁移，得到 `duplicate column name`，容器进入崩溃循环。",
		"**已应用的迁移文件绝不能改名或编辑。** 历史按**文件名**记录，改名等于让它在既有库上重新执行。",
		"加列要新开一个 `NNN_描述.sql`，用 `ALTER TABLE ... ADD COLUMN`。" +
			"SQLite 不支持 `ADD COLUMN IF NOT EXISTS`，先查 `schema_migrations` 是否已应用。",
		"新增迁移后要同步更新 `store_test.go` 里的迁移数量断言。",
	}))

	b.WriteString(heading(2, "时间格式因表而异"))
	b.WriteString(paragraph(
		"写种子脚本或修数脚本时混用会静默变成零值时间：",
	))
	b.WriteString(table([]string{"格式", "表"}, [][]string{
		{"`YYYY-MM-DD HH:MM:SS`", "`proxy_logs`、`usage_records`、`model_health`"},
		{"RFC3339Nano", "`balance_history`、`channel_health_history`、`discovered_models`、`audit_events`"},
	}))
	b.WriteString("\n")

	b.WriteString(heading(2, "表"))
	b.WriteString(countNote(len(tables), "表"))
	for _, t := range tables {
		b.WriteString(heading(3, t.name+" · "+itoa(len(t.columns))+" 列"))
		var rows [][]string
		for _, col := range t.columns {
			rows = append(rows, []string{
				"`" + col.name + "`",
				orDash(col.typeName),
				boolMark(col.notNull),
				orDash(col.defaultValue),
				boolMark(col.primaryKey),
			})
		}
		b.WriteString(table([]string{"列", "类型", "NOT NULL", "默认值", "主键"}, rows))
		b.WriteString("\n")
	}

	b.WriteString(heading(2, "索引"))
	b.WriteString(countNote(len(indexes), "索引"))
	var idxRows [][]string
	for _, idx := range indexes {
		idxRows = append(idxRows, []string{"`" + idx.name + "`", "`" + idx.table + "`", boolMark(idx.unique)})
	}
	b.WriteString(table([]string{"索引", "表", "唯一"}, idxRows))
	b.WriteString("\n")

	return b.String(), nil
}

type schemaTable struct {
	name    string
	columns []schemaColumn
}

type schemaColumn struct {
	name         string
	typeName     string
	notNull      bool
	defaultValue string
	primaryKey   bool
}

type schemaIndex struct {
	name   string
	table  string
	unique bool
}

func listTables(db *store.DB) ([]schemaTable, error) {
	rows, err := db.Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tables := make([]schemaTable, 0, len(names))
	for _, name := range names {
		columns, err := tableColumns(db, name)
		if err != nil {
			return nil, err
		}
		tables = append(tables, schemaTable{name: name, columns: columns})
	}
	return tables, nil
}

func tableColumns(db *store.DB, table string) ([]schemaColumn, error) {
	// The table name comes from sqlite_master, never from user input, and
	// PRAGMA does not accept a bind parameter.
	rows, err := db.Query(`PRAGMA table_info(` + quoteIdent(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []schemaColumn
	for rows.Next() {
		var (
			cid          int
			name         string
			typeName     string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		out = append(out, schemaColumn{
			name:         name,
			typeName:     typeName,
			notNull:      notNull != 0,
			defaultValue: strings.TrimSpace(defaultValue.String),
			primaryKey:   pk != 0,
		})
	}
	return out, rows.Err()
}

func listIndexes(db *store.DB) ([]schemaIndex, error) {
	rows, err := db.Query(
		`SELECT name, tbl_name, sql FROM sqlite_master
		  WHERE type = 'index' AND name NOT LIKE 'sqlite_%' ORDER BY tbl_name, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []schemaIndex
	for rows.Next() {
		var name, table string
		var ddl sql.NullString
		if err := rows.Scan(&name, &table, &ddl); err != nil {
			return nil, err
		}
		out = append(out, schemaIndex{
			name:   name,
			table:  table,
			unique: strings.Contains(strings.ToUpper(ddl.String), "UNIQUE"),
		})
	}
	return out, rows.Err()
}

func appliedMigrationCount(db *store.DB) (int, error) {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func migrationFileCount(dir string) (int, error) {
	names, err := readDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, name := range names {
		if strings.HasSuffix(name, ".sql") {
			count++
		}
	}
	return count, nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func boolMark(value bool) string {
	if value {
		return "是"
	}
	return "—"
}

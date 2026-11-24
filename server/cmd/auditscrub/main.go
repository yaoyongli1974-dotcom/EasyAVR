// Command auditscrub triages and (explicitly) redacts historical audit request bodies.
// It is a manual tool: by default it only reports; redaction requires --apply --yes --backup.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/easyavr/easyavr/internal/audittool"
)

func main() {
	var opts audittool.Options
	flag.StringVar(&opts.Driver, "driver", "sqlite", "database driver: sqlite or postgres")
	flag.StringVar(&opts.DSN, "dsn", "", "sqlite file path or postgres DSN")
	flag.StringVar(&opts.BackupPath, "backup", "", "sqlite: backup file to create; postgres: existing pg_dump file")
	flag.BoolVar(&opts.Apply, "apply", false, "actually redact (default: dry-run only)")
	flag.BoolVar(&opts.Confirm, "yes", false, "confirm redaction (required together with --apply)")
	flag.IntVar(&opts.Limit, "limit", 500, "maximum rows to redact per run")
	flag.Parse()

	rep, err := audittool.Run(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "auditscrub:", err)
		os.Exit(1)
	}
	if opts.Apply {
		fmt.Printf("已脱敏 %d 条，扫描命中 %d 条，备份：%s\n", rep.Redacted, rep.Suspicious, rep.Backup)
		return
	}
	fmt.Printf("命中疑似敏感审计记录 %d 条（未修改数据），样例 ID：%v\n", rep.Suspicious, rep.SampleIDs)
	fmt.Println("如需脱敏：先备份数据库，再加 --apply --yes --backup <文件> 重跑。")
}

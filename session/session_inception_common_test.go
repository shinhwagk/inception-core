// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package session_test

import (
	"errors"
	"flag"
	"fmt"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/hanchuanchuan/inception-core/ast"
	"github.com/hanchuanchuan/inception-core/config"
	"github.com/hanchuanchuan/inception-core/parser"
	"github.com/hanchuanchuan/inception-core/session"
	"github.com/hanchuanchuan/inception-core/util/logutil"
	"github.com/jinzhu/gorm"
	. "github.com/pingcap/check"

	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
)

var _ = Suite(&testCommon{})

var sql string

// 是否测试api接口
var isAPI bool

func init() {
	flag.BoolVar(&isAPI, "api", false, "test api interface")
}

func TestCommonTest(t *testing.T) {
	TestingT(t)
}

type testCommon struct {
	// store  kv.Storage
	db     *gorm.DB
	dbAddr string

	// 执行结果集
	// res *testkit.Result
	rows [][]interface{}

	DBVersion         int
	sqlMode           string
	innodbLargePrefix bool
	// 时间戳类型是否需要明确指定默认值
	explicitDefaultsForTimestamp bool
	// 是否忽略大小写(lower_case_table_names为1和2时忽略,否则不忽略)
	ignoreCase bool

	realRowCount bool

	remoteBackupTable string
	parser            *parser.Parser

	// session session.Session

	defaultInc config.Inc

	// 测试数据库,默认为test_inc,该参数用以测试未指定数据库情况下的审核
	useDB string

	// API调用
	isAPI bool
	// session API调用
	sessionService session.Session
	// API返回结果
	records []session.Record
}

func (s *testCommon) initSetUp(c *C) {

	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	}

	flag.Parse()

	log.SetLevel(log.ErrorLevel)

	s.realRowCount = true

	s.useDB = "use test_inc;"

	// server.InitOscProcessList()

	cfg := config.GetGlobalConfig()
	_, localFile, _, _ := runtime.Caller(0)
	localFile = path.Dir(localFile)
	configFile := path.Join(localFile[0:len(localFile)-len("session")], "config/config.toml.example")
	c.Assert(cfg.Load(configFile), IsNil)

	inc := &config.GetGlobalConfig().Inc

	inc.BackupHost = "127.0.0.1"
	inc.BackupPort = 3306
	inc.BackupUser = "test"
	inc.BackupPassword = "test"

	inc.Lang = "en-US"
	inc.EnableFingerprint = true
	inc.SqlSafeUpdates = 0
	inc.EnableDropTable = true

	// mysql5.6测试用例会出错(docker映射对外的端口不一致)
	config.GetGlobalConfig().Ghost.GhostAliyunRds = true

	s.defaultInc = *inc

	s.remoteBackupTable = "$_$Inception_backup_information$_$"
	s.parser = parser.New()

	c.Assert(s.mysqlServerVersion(), IsNil)
	c.Assert(s.sqlMode, Not(Equals), "")
	// log.Infof("%#v", s)
	log.Error("数据库版本: ", s.DBVersion)

	// 测试API接口时自动忽略之前的测试方法
	s.isAPI = isAPI
	s.isAPI = true
	s.sessionService = session.NewInception()
	s.sessionService.LoadOptions(session.SourceOptions{
		Host:         inc.BackupHost,
		Port:         int(inc.BackupPort),
		User:         inc.BackupUser,
		Password:     inc.BackupPassword,
		RealRowCount: s.realRowCount,
	})
}

func (s *testCommon) tearDownSuite(c *C) {
	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	} else {
		// testleak.AfterTest(c)()
		if s.db != nil {
			s.db.Close()
		}
	}
}

func (s *testCommon) tearDownTest(c *C) {
	if testing.Short() {
		c.Skip("skipping test; in TRAVIS mode")
	}
	saved := config.GetGlobalConfig().Inc
	defer func() {
		config.GetGlobalConfig().Inc = saved
	}()

	config.GetGlobalConfig().Inc.EnableDropTable = true
	session.CheckAuditSetting(config.GetGlobalConfig())

	s.runCheck("show tables")
	c.Assert(int(s.getAffectedRows()), Equals, 2)

	row := s.rows[s.getAffectedRows()-1]
	sql := row[5]

	// 	exec := `/*%s;--execute=1;--backup=0;--enable-ignore-warnings;*/
	// inception_magic_start;
	// %s
	// %s;
	// inception_magic_commit;`
	for _, name := range strings.Split(sql.(string), "\n") {
		if strings.HasPrefix(name, "show tables") {
			continue
		}
		n := strings.Replace(name, "'", "", -1)

		s.mustRunExec(c, "drop table `"+n+"`")
		// log.Info(res.Rows())
		c.Assert(s.getAffectedRows(), Equals, 2)
		row := s.rows[s.getAffectedRows()-1]
		// c.Assert(row[2], Equals, "0", Commentf("%v", row))
		c.Assert(row[3], Equals, "Execute Successfully", Commentf("%v", row))
		// c.Assert(err, check.IsNil, check.Commentf("sql:%s, %v, error stack %v", sql, args, errors.ErrorStack(err)))
		// log.Info(row[4])
		// c.Assert(row[4].(string), IsNil)
	}

}

func (s *testCommon) runCheck(sql string) {
	session.CheckAuditSetting(config.GetGlobalConfig())

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		IgnoreWarnings: true,
	})
	result, _ := s.sessionService.Audit(context.Background(), s.useDB+sql)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		// c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}
}

func (s *testCommon) mustCheck(c *C, sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:         s.defaultInc.BackupHost,
		Port:         int(s.defaultInc.BackupPort),
		User:         s.defaultInc.BackupUser,
		Password:     s.defaultInc.BackupPassword,
		RealRowCount: s.realRowCount,
	})
	result, err := s.sessionService.Audit(context.Background(), s.useDB+sql)
	c.Assert(err, IsNil)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}

}

func (s *testCommon) runExec(sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		IgnoreWarnings: true,
	})
	result, _ := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		// c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}

}

func (s *testCommon) mustRunExec(c *C, sql string) {
	config.GetGlobalConfig().Inc.EnableDropTable = true
	session.CheckAuditSetting(config.GetGlobalConfig())

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		IgnoreWarnings: true,
	})
	result, err := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	c.Assert(err, IsNil)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}

}

func (s *testCommon) runBackup(sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		Backup:         true,
		IgnoreWarnings: true,
	})
	result, _ := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		// c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}

}

func (s *testCommon) mustRunBackup(c *C, sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		Backup:         true,
		IgnoreWarnings: true,
	})
	result, err := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	c.Assert(err, IsNil)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}
}

func (s *testCommon) mustRunBackupTran(c *C, sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		Backup:         true,
		IgnoreWarnings: true,
		TranBatch:      3,
	})
	result, err := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	c.Assert(err, IsNil)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}
}

func (s *testCommon) runTranSQL(sql string, batch int) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		Backup:         true,
		IgnoreWarnings: true,
		TranBatch:      batch,
	})
	result, _ := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		s.rows[index] = row.List()
	}
}

func (s *testCommon) mustrunTranSQL(c *C, sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		Backup:         true,
		IgnoreWarnings: true,
		TranBatch:      10,
	})
	result, _ := s.sessionService.RunExecute(context.Background(), s.useDB+sql)
	s.records = result
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		c.Assert(row.ErrLevel, Not(Equals), uint8(2), Commentf("%v", result))
		s.rows[index] = row.List()
	}
}

func (s *testCommon) getAddr() string {
	if s.dbAddr != "" {
		return s.dbAddr
	}
	inc := config.GetGlobalConfig().Inc
	s.dbAddr = fmt.Sprintf(`--host=%s;--port=%d;--user=%s;--password=%s;`,
		inc.BackupHost, inc.BackupPort, inc.BackupUser, inc.BackupPassword)
	return s.dbAddr
}

func (s *testCommon) mysqlServerVersion() error {
	inc := config.GetGlobalConfig().Inc
	if s.db == nil || s.db.DB().Ping() != nil {
		addr := fmt.Sprintf("%s:%s@tcp(%s:%d)/mysql?charset=utf8mb4&parseTime=True&loc=Local&maxAllowedPacket=4194304",
			inc.BackupUser, inc.BackupPassword, inc.BackupHost, inc.BackupPort)

		db, err := gorm.Open("mysql", addr)
		if err != nil {
			return err
		}
		// 禁用日志记录器，不显示任何日志
		db.LogMode(false)
		s.db = db
	}

	var name, value string
	sql := `show variables where Variable_name in
		('explicit_defaults_for_timestamp','innodb_large_prefix','version','sql_mode','lower_case_table_names');`
	rows, err := s.db.Raw(sql).Rows()
	if err != nil {
		return err
	}
	// emptyInnodbLargePrefix := true
	for rows.Next() {
		rows.Scan(&name, &value)

		switch name {
		case "version":
			versionStr := strings.Split(value, "-")[0]
			versionSeg := strings.Split(versionStr, ".")
			if len(versionSeg) == 3 {
				versionStr = fmt.Sprintf("%s%02s%02s", versionSeg[0], versionSeg[1], versionSeg[2])
				version, err := strconv.Atoi(versionStr)
				if err != nil {
					return err
				}
				s.DBVersion = version
			} else {
				return errors.New(fmt.Sprintf("无法解析版本号:%s", value))
			}
			log.Debug("db version: ", s.DBVersion)
		case "innodb_large_prefix":
			// emptyInnodbLargePrefix = false
			s.innodbLargePrefix = value == "ON" || value == "1"
		case "sql_mode":
			s.sqlMode = value
		case "lower_case_table_names":
			if v, err := strconv.Atoi(value); err != nil {
				return err
			} else {
				s.ignoreCase = v > 0
			}
		case "explicit_defaults_for_timestamp":
			if value == "ON" {
				s.explicitDefaultsForTimestamp = true
			}
		}
	}

	// 如果没有innodb_large_prefix系统变量
	// if emptyInnodbLargePrefix {
	// 	if s.DBVersion > 50700 {
	// 		s.innodbLargePrefix = true
	// 	} else {
	// 		s.innodbLargePrefix = false
	// 	}
	// }
	return nil
}

func (s *testCommon) assertRows(c *C, rows [][]interface{}, rollbackSqls ...string) error {
	c.Assert(len(rows), Not(Equals), 0)
	inc := config.GetGlobalConfig().Inc
	if s.db == nil || s.db.DB().Ping() != nil {
		addr := fmt.Sprintf("%s:%s@tcp(%s:%d)/mysql?charset=utf8mb4&parseTime=True&loc=Local&maxAllowedPacket=4194304",
			inc.BackupUser, inc.BackupPassword, inc.BackupHost, inc.BackupPort)

		db, err := gorm.Open("mysql", addr)
		if err != nil {
			log.Info(err)
			return err
		}
		// 禁用日志记录器，不显示任何日志
		db.LogMode(false)
		s.db = db
	}

	// 有可能是 不同的表,不同的库

	result := []string{}

	// affectedRows := 0
	// opid := ""
	// backupDBName := ""
	// sqlIndex := 0

	var lastTable, currentTable string
	var ids []string
	for _, row := range rows {
		opid := ""
		backupDBName := ""

		// runSql := ""
		// if row[5] != nil {
		// 	runSql = row[5].(string)
		// }

		// affectedRows := 0
		// if row[6] != nil {
		// 	a := row[6].(string)
		// 	affectedRows, _ = strconv.Atoi(a)
		// }

		if row[7] != nil {
			opid = row[7].(string)
		}
		if row[8] != nil {
			backupDBName = row[8].(string)
		}
		currentSql := ""
		if row[5] != nil {
			currentSql = row[5].(string)
		}

		if !strings.Contains(row[3].(string), "Backup Successfully") || strings.HasSuffix(opid, "00000000") {
			continue
		}

		tableName := s.getObjectName(currentSql)
		// 表名没有时,查询一下
		if tableName == "" {
			sql := "select tablename from `%s`.`%s` where opid_time = ?"
			sql = fmt.Sprintf(sql, backupDBName, s.remoteBackupTable)
			tableRows, err := s.db.Raw(sql, opid).Rows()
			c.Assert(err, IsNil)
			for tableRows.Next() {
				tableRows.Scan(&tableName)
			}
			tableRows.Close()
		}
		c.Assert(tableName, Not(Equals), "", Commentf("%v", row))

		currentTable = fmt.Sprintf("%s.`%s`", backupDBName, tableName)
		if lastTable == "" {
			lastTable = currentTable
		}

		// 如果表改变了,或者超过500行了
		if lastTable != currentTable || len(ids) >= 500 {
			if len(ids) > 0 {
				sql := "select rollback_statement from %s where opid_time in (?) order by opid_time,id;"
				sql = fmt.Sprintf(sql, lastTable)
				rows, err := s.db.Raw(sql, ids).Rows()
				c.Assert(err, IsNil)

				str := ""
				result1 := []string{}
				for rows.Next() {
					rows.Scan(&str)
					result1 = append(result1, s.trim(str))
				}
				rows.Close()

				c.Assert(len(result1), Not(Equals), 0, Commentf("-----------: %v,%v", sql, ids))
				result = append(result, result1...)

				ids = nil
			}
			lastTable = currentTable
		}

		ids = append(ids, opid)
	}

	// for循环可能提前退出,所以最后的查询放在外面
	if len(ids) > 0 {
		sql := "select rollback_statement from %s where opid_time in (?) order by opid_time,id;"
		sql = fmt.Sprintf(sql, currentTable)
		rollbackRows, err := s.db.Raw(sql, ids).Rows()
		c.Assert(err, IsNil)

		str := ""
		result1 := []string{}
		for rollbackRows.Next() {
			rollbackRows.Scan(&str)
			result1 = append(result1, s.trim(str))
		}
		rollbackRows.Close()

		c.Assert(len(result1), Not(Equals), 0, Commentf("------2-----: %v", rows))
		result = append(result, result1...)
	}

	c.Assert(len(result), Equals, len(rollbackSqls), Commentf("%v", result))

	// 如果是UPDATE多表操作,此时回滚的SQL可能是无序的
	if len(result) > 1 && strings.HasPrefix(result[0], "UPDATE") {
		prefix := ""
		for i, sql := range result {
			if i == 0 {
				prefix = strings.Fields(sql)[1]
				continue
			}
			if prefix != strings.Fields(sql)[1] {
				sort.Strings(result)
				sort.Strings(rollbackSqls)
				break
			}
		}
	}

	for i := range result {
		c.Assert(result[i], Equals, rollbackSqls[i], Commentf("%v", result))
	}

	return nil
}

func (s *testCommon) trim(str string) string {
	if strings.Contains(str, "  ") {
		return s.trim(strings.Replace(str, "  ", " ", -1))
	}
	return str
}

func getLeftTable(node ast.ResultSetNode) *ast.TableSource {
	if node == nil {
		return nil
	}
	log.Infof("%T", node)
	switch x := node.(type) {
	case *ast.Join:
		return getLeftTable(x.Left)
	case *ast.TableSource:
		return x
	case *ast.SelectStmt:
		if x.From != nil {
			return getLeftTable(x.From.TableRefs)
		}
	case *ast.UnionStmt:
		for _, sel := range x.SelectList.Selects {
			return getLeftTable(sel)
		}
	}
	return nil
}

// getObjectName 解析操作表名
func (s *testCommon) getObjectName(sql string) (name string) {

	stmtNodes, _, _ := s.parser.Parse(sql, "utf8mb4", "utf8mb4_bin")

	for _, stmtNode := range stmtNodes {
		switch node := stmtNode.(type) {
		case *ast.InsertStmt:
			tableRefs := node.Table
			if tableRefs == nil || tableRefs.TableRefs == nil || tableRefs.TableRefs.Right != nil {
				return ""
			}
			tblSrc, ok := tableRefs.TableRefs.Left.(*ast.TableSource)
			if !ok {
				return ""
			}
			if tblSrc.AsName.L != "" {
				return ""
			}
			tblName, ok := tblSrc.Source.(*ast.TableName)
			if !ok {
				return ""
			}

			name = tblName.Name.String()

		case *ast.UpdateStmt:
			return ""

			tblSrc := getLeftTable(node.TableRefs.TableRefs)
			if tblSrc == nil {
				log.Errorf("未找到表名！！！ sql: %s", sql)
				return ""
			}
			tblName, ok := tblSrc.Source.(*ast.TableName)
			if !ok {
				log.Infof("%#v", tblSrc.Source)
				return ""
			}

			// for _, l := range node.List {
			// 	originTable := l.Column.Table.L
			// 	firstColumnName := l.Column.Name.O

			// }

			name = tblName.Name.String()
		case *ast.DeleteStmt:
			tableRefs := node.TableRefs
			if tableRefs == nil || tableRefs.TableRefs == nil || tableRefs.TableRefs.Right != nil {
				return ""
			}
			tblSrc, ok := tableRefs.TableRefs.Left.(*ast.TableSource)
			if !ok {
				return ""
			}
			if tblSrc.AsName.L != "" {
				return ""
			}
			tblName, ok := tblSrc.Source.(*ast.TableName)
			if !ok {
				return ""
			}

			name = tblName.Name.String()

		case *ast.CreateDatabaseStmt, *ast.DropDatabaseStmt:

		case *ast.CreateTableStmt:
			name = node.Table.Name.String()
		case *ast.AlterTableStmt:
			name = node.Table.Name.String()
		case *ast.DropTableStmt:
			for _, t := range node.Tables {
				name = t.Name.String()
				break
			}

		case *ast.RenameTableStmt:
			name = node.OldTable.Name.String()

		case *ast.TruncateTableStmt:

			name = node.Table.Name.String()

		case *ast.CreateIndexStmt:
			name = node.Table.Name.String()
		case *ast.DropIndexStmt:
			name = node.Table.Name.String()

		default:

		}

		return name
	}
	return ""
}

func (s *testCommon) queryStatistics() []int {
	inc := config.GetGlobalConfig().Inc
	if s.db == nil || s.db.DB().Ping() != nil {

		addr := fmt.Sprintf("%s:%s@tcp(%s:%d)/mysql?charset=utf8mb4&parseTime=True&loc=Local&maxAllowedPacket=4194304",
			inc.BackupUser, inc.BackupPassword, inc.BackupHost, inc.BackupPort)
		db, err := gorm.Open("mysql", addr)
		if err != nil {
			fmt.Println(err)
		}
		// 禁用日志记录器，不显示任何日志
		db.LogMode(false)
		s.db = db
	}

	sql := `select usedb, deleting, inserting, updating,
		selecting, altertable, renaming, createindex, dropindex, addcolumn,
		dropcolumn, changecolumn, alteroption, alterconvert,
		createtable, droptable, CREATEDB, truncating from inception.statistic order by id desc limit 1;`
	values := make([]int, 18)

	rows, err := s.db.Raw(sql).Rows()
	if err != nil {
		fmt.Println(err)
		panic(err)
	} else {
		defer rows.Close()
		for rows.Next() {
			rows.Scan(&values[0],
				&values[1],
				&values[2],
				&values[3],
				&values[4],
				&values[5],
				&values[6],
				&values[7],
				&values[8],
				&values[9],
				&values[10],
				&values[11],
				&values[12],
				&values[13],
				&values[14],
				&values[15],
				&values[16],
				&values[17])
		}
	}
	return values
}

func trim(s string) string {
	if strings.Contains(s, "  ") {
		return trim(strings.Replace(s, "  ", " ", -1))
	}
	return s
}

func (s *testCommon) query(table, opid string) string {
	inc := config.GetGlobalConfig().Inc
	if s.db == nil || s.db.DB().Ping() != nil {
		addr := fmt.Sprintf("%s:%s@tcp(%s:%d)/mysql?charset=utf8mb4&parseTime=True&loc=Local&maxAllowedPacket=4194304",
			inc.BackupUser, inc.BackupPassword, inc.BackupHost, inc.BackupPort)

		db, err := gorm.Open("mysql", addr)
		if err != nil {
			fmt.Println(err)
			return err.Error()
		}
		// 禁用日志记录器，不显示任何日志
		db.LogMode(false)
		s.db = db
	}

	result := []string{}
	sql := "select rollback_statement from 127_0_0_1_%d_test_inc.`%s` where opid_time = ?;"
	sql = fmt.Sprintf(sql, inc.BackupPort, table)

	rows, err := s.db.Raw(sql, opid).Rows()
	if err != nil {
		fmt.Println(err)
		return err.Error()
	} else {
		defer rows.Close()
		for rows.Next() {
			str := ""
			rows.Scan(&str)
			result = append(result, trim(str))
		}
	}
	return strings.Join(result, "\n")
}

// parserStmt 解析sql变成ast语法
func (s *testCommon) parserStmt(sql string) ast.StmtNode {
	stmtNodes, _, _ := s.parser.Parse(sql, "utf8mb4", "utf8mb4_bin")
	for _, stmtNode := range stmtNodes {
		return stmtNode
	}
	return nil
}

func (s *testCommon) reset() {
	config.GetGlobalConfig().Inc = s.defaultInc
	log.SetLevel(log.ErrorLevel)
	// log.SetReportCaller(true)
	log.AddHook(&logutil.ContextHook{})
}

// getAffectedRows 获取受影响行数, 区分api返回和mysql会话返回
func (s *testCommon) getAffectedRows() int {
	return len(s.rows)
	// if s.isAPI {
	// 	return len(s.rows)
	// }
	// return int(s.session.AffectedRows())
}

func (s *testCommon) testAffectedRows(c *C, affectedRows ...int) {
	if len(s.rows) == 0 && len(s.records) == 0 {
		return
	}
	count := len(affectedRows)
	for i, affectedRow := range affectedRows {
		if s.isAPI {
			row := s.records[len(s.records)-(count-i)]
			c.Assert(row.AffectedRows, Equals, affectedRow, Commentf("%#v", row))
		} else {
			row := s.rows[len(s.rows)-(count-i)]
			c.Assert(row[6], Equals, strconv.Itoa(affectedRow), Commentf("%v", row))
		}
	}
}

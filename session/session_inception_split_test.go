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
	"strconv"

	// "strings"
	"testing"

	"github.com/hanchuanchuan/inception-core/config"
	"github.com/hanchuanchuan/inception-core/session"
	. "github.com/pingcap/check"
	"golang.org/x/net/context"
)

var _ = Suite(&testSessionSplitSuite{})

func TestSplit(t *testing.T) {
	TestingT(t)
}

type testSessionSplitSuite struct {
	testCommon
}

func (s *testSessionSplitSuite) SetUpSuite(c *C) {

	s.initSetUp(c)

	config.GetGlobalConfig().Inc.Lang = "en-US"
	config.GetGlobalConfig().Inc.EnableFingerprint = true
	config.GetGlobalConfig().Inc.SqlSafeUpdates = 0

}

func (s *testSessionSplitSuite) TearDownSuite(c *C) {
	s.tearDownSuite(c)
}

func (s *testSessionSplitSuite) makeSQL(sql string) {

	s.sessionService.LoadOptions(session.SourceOptions{
		Host:           s.defaultInc.BackupHost,
		Port:           int(s.defaultInc.BackupPort),
		User:           s.defaultInc.BackupUser,
		Password:       s.defaultInc.BackupPassword,
		RealRowCount:   s.realRowCount,
		IgnoreWarnings: true,
	})
	result, _ := s.sessionService.Split(context.Background(), s.useDB+sql)
	s.rows = make([][]interface{}, len(result))
	for index, row := range result {
		s.rows[index] = row.List()
	}
}

// func (s *testSessionSplitSuite) TestWrongStmt(c *C) {
// 	sql := `/*--user=test;--password=test;--host=127.0.0.1;--split=1;--port=3306;--enable-ignore-warnings;*/
// inception_magic_start;
// 123;
// inception_magic_commit;`
// 	res := s.tk.MustQueryInc(sql)

// 	c.Assert(int(s.tk.Se.AffectedRows()), Equals, 1)
// 	row := s.rows[s.tk.Se.AffectedRows()-1]
// 	// c.Assert(row[3], Equals, "line 1 column 3 near \"\" (total length 3)")
// 	c.Assert(row[3], Equals, "You have an error in your SQL syntax, near '123' at line 1")
// }

func (s *testSessionSplitSuite) TestInsert(c *C) {

	s.testRows(c, "insert into t1 values(1);", 1)
}

func (s *testSessionSplitSuite) TestUpdate(c *C) {

	s.testRows(c, `update t1 set c1=1 where a=1;`, 1)
}

func (s *testSessionSplitSuite) TestDelete(c *C) {

	s.testRows(c, `delete from t1 where id=1;`, 1)

}

func (s *testSessionSplitSuite) TestAlterTable(c *C) {

	s.testRows(c, `alter table t1 add column c1 int;`, 1)

}

func (s *testSessionSplitSuite) TestMoreSql(c *C) {

	s.testRows(c, `
drop table if exists t1;
create table t1 like test.t1;
alter table t2 add column c1 int;
insert into t3(c1) values(1);
insert into t1(c1) values(1);
truncate table t1;
truncate table t2;

truncate table t3;

desc t1;
insert into t3(c1) values(1);
insert into t1(c1) values(1);

alter table t1 add column c1 int;
drop table if exists table1;drop table if exists table2;
create table table1(id1 int primary key,c1 int);
create table table2(id2 int primary key,c2 int,c22 int);

update table1 a1,table2 a2 set a1.c1=a2.c2 where a1.id1=a2.id2 and a1.c1=a2.c2 and a1.id1 in (1,2,3);
alter table table1 add column c10 int;
update table1 a1,table2 a2 set a2.c1=a2.c2 where a1.id1=a2.id2 and a1.c1=a2.c2 and a1.id1 in (1,2,3);
alter table table2 add column c10 int;
update table1 a1,table2 a2 set a2.c1=a2.c2 where a1.id1=a2.id2 and a1.c1=a2.c2 and a1.id1 in (1,2,3);`, 7)

	flags := []int{1, 0, 1, 0, 1, 1, 0}
	for i, row := range s.rows {
		c.Assert(row[2], Equals, strconv.Itoa(flags[i]), Commentf("%v", row))
	}

}

func (s *testSessionSplitSuite) testRows(c *C, sql string, count int) {

	if s.isAPI {
		s.sessionService.LoadOptions(session.SourceOptions{
			Host:         s.defaultInc.BackupHost,
			Port:         int(s.defaultInc.BackupPort),
			User:         s.defaultInc.BackupUser,
			Password:     s.defaultInc.BackupPassword,
			RealRowCount: s.realRowCount,
		})

		result, err := s.sessionService.Split(context.Background(), s.useDB+sql)
		c.Assert(err, IsNil)

		c.Assert(len(result), Equals, count, Commentf("%v", result))

		// s.rows = make([][]interface{}, len(result))
		for _, row := range result {
			c.Assert(row.ErrorMessage, Equals, "", Commentf("%v", row))
		}
		return
	}

	s.makeSQL(sql)
	c.Assert(len(s.rows), Equals, count, Commentf("%v", s.rows))
	c.Assert(int(s.getAffectedRows()), Equals, count, Commentf("%v", s.rows))

	for _, row := range s.rows {
		c.Assert(row[3], Equals, "<nil>", Commentf("%v", row))
	}

	s.rows = s.rows
}

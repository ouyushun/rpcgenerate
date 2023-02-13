package core

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"rpcgenerate/tools/stringx"
	"sort"
	"strings"

	"github.com/chuckpreslar/inflect"
	"github.com/serenize/snaker"
)

const (
	// proto3 is a describing the proto3 syntax type.
	proto3 = "proto3"

	// indent represents the indentation amount for fields. the style guide suggests
	// two spaces
	indent  = "    "
	indent2 = "        "
	indent3 = "            "
	indent4 = "                "
	indent5 = "                    "
	// gen protobuf field style
	fieldStyleToCamelWithStartLower = "sqlPb"
	fieldStyleToSnake               = "sql_pb"
)

// GenerateProto generates a protobuf schema from a database connection and a package name.
// A list of tables to ignore may also be supplied.
// The returned schema implements the `fmt.Stringer` interface, in order to generate a string
// representation of a protobuf schema.
// Do not rely on the structure of the Generated schema to provide any context about
// the protobuf types. The schema reflects the layout of a protobuf file and should be used
// to pipe the output of the `Schema.String()` to a file.
func GenerateProto(db *sql.DB, table string, ignoreTables, ignoreColumns []string, serviceName, pkg, fieldStyle, dbType string) (*Schema, error) {
	s := &Schema{}

	dbs, err := dbSchema(db, dbType)
	if nil != err {
		return nil, err
	}

	s.Syntax = proto3
	s.ServiceName = serviceName
	if "" != pkg {
		s.Package = pkg
	}

	cols, err := dbColumns(db, dbs, table, dbType)
	if nil != err {
		return nil, err
	}

	err = typesFromColumns(s, cols, ignoreTables, ignoreColumns, fieldStyle)
	if nil != err {
		return nil, err
	}
	sort.Sort(s.Imports)
	sort.Sort(s.Messages)
	sort.Sort(s.Enums)

	return s, nil
}

// typesFromColumns creates the appropriate schema properties from a collection of column types.
func typesFromColumns(s *Schema, cols []Column, ignoreTables, ignoreColumns []string, fieldStyle string) error {
	messageMap := map[string]*Message{}
	ignoreMap := map[string]bool{}
	ignoreColumnMap := map[string]bool{}
	for _, ig := range ignoreTables {
		ignoreMap[ig] = true
	}
	for _, ic := range ignoreColumns {
		ignoreColumnMap[ic] = true
	}

	for _, c := range cols {
		if _, ok := ignoreMap[c.TableName]; ok {
			continue
		}
		if _, ok := ignoreColumnMap[c.ColumnName]; ok {
			continue
		}

		messageName := snaker.SnakeToCamel(c.TableName)
		//messageName = inflect.Singularize(messageName)

		msg, ok := messageMap[messageName]
		if !ok {
			messageMap[messageName] = &Message{Name: messageName, Comment: c.TableComment, Style: fieldStyle}
			msg = messageMap[messageName]
		}

		err := parseColumn(s, msg, c)
		if nil != err {
			return err
		}
	}

	for _, v := range messageMap {
		s.Messages = append(s.Messages, v)
	}

	return nil
}

func dbSchema(db *sql.DB, dbType string) (string, error) {
	var schema string
	switch dbType {

	case "mysql":
		err := db.QueryRow("SELECT SCHEMA()").Scan(&schema)
		return schema, err
	case "sqlserver":
		err := db.QueryRow("Select top 1 Name From Master..SysDataBases Where DbId=(Select Dbid From Master..SysProcesses Where Spid = @@spid)").Scan(&schema)
		return schema, err
	}
	return schema, nil
}

func dbColumns(db *sql.DB, schema, table, dbType string) ([]Column, error) {

	tableArr := strings.Split(table, ",")
	q := ""
	switch dbType {
	case "mysql":
		q = "SELECT c.TABLE_NAME, c.COLUMN_NAME, c.IS_NULLABLE, c.DATA_TYPE, " +
			"c.CHARACTER_MAXIMUM_LENGTH, c.NUMERIC_PRECISION, c.NUMERIC_SCALE, c.COLUMN_TYPE ,c.COLUMN_COMMENT,t.TABLE_COMMENT " +
			"FROM INFORMATION_SCHEMA.COLUMNS as c  LEFT JOIN  INFORMATION_SCHEMA.TABLES as t  on c.TABLE_NAME = t.TABLE_NAME and  c.TABLE_SCHEMA = t.TABLE_SCHEMA" +
			" WHERE c.TABLE_SCHEMA = ?"

	case "sqlserver":
		q = "SELECT c.TABLE_NAME, c.COLUMN_NAME, c.IS_NULLABLE, c.DATA_TYPE, " +
			"c.CHARACTER_MAXIMUM_LENGTH, c.NUMERIC_PRECISION, c.NUMERIC_SCALE,  c.Data_TYPE AS COLUMN_TYPE,'' as COLUMN_COMMENT,'' as TABLE_COMMENT" +
			"FROM INFORMATION_SCHEMA.COLUMNS as c  LEFT JOIN  INFORMATION_SCHEMA.TABLES as t  on c.TABLE_NAME = t.TABLE_NAME and  c.TABLE_SCHEMA = t.TABLE_SCHEMA" +
			" WHERE c.TABLE_CATALOG = ?"
	}
	if table != "" && table != "*" {
		q += " AND c.TABLE_NAME IN('" + strings.TrimRight(strings.Join(tableArr, "' ,'"), ",") + "')"
	}
	q += " ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION"
	rows, err := db.Query(q, schema)
	defer rows.Close()
	if nil != err {
		return nil, err
	}

	cols := []Column{}

	for rows.Next() {
		cs := Column{}
		err := rows.Scan(&cs.TableName, &cs.ColumnName, &cs.IsNullable, &cs.DataType,
			&cs.CharacterMaximumLength, &cs.NumericPrecision, &cs.NumericScale, &cs.ColumnType, &cs.ColumnComment, &cs.TableComment)
		if err != nil {
			log.Fatal(err)
		}

		if cs.TableComment == "" {
			cs.TableComment = stringx.From(cs.TableName).ToCamelWithStartLower()
		}

		cols = append(cols, cs)
	}
	if err := rows.Err(); nil != err {
		return nil, err
	}

	return cols, nil
}

// Schema is a representation of a protobuf schema.
type Schema struct {
	Syntax      string
	ServiceName string
	Package     string
	Imports     sort.StringSlice
	Messages    MessageCollection
	Enums       EnumCollection
}

// MessageCollection represents a sortable collection of messages.
type MessageCollection []*Message

func (mc MessageCollection) Len() int {
	return len(mc)
}

func (mc MessageCollection) Less(i, j int) bool {
	return mc[i].Name < mc[j].Name
}

func (mc MessageCollection) Swap(i, j int) {
	mc[i], mc[j] = mc[j], mc[i]
}

// EnumCollection represents a sortable collection of enums.
type EnumCollection []*Enum

func (ec EnumCollection) Len() int {
	return len(ec)
}

func (ec EnumCollection) Less(i, j int) bool {
	return ec[i].Name < ec[j].Name
}

func (ec EnumCollection) Swap(i, j int) {
	ec[i], ec[j] = ec[j], ec[i]
}

// AppendImport adds an import to a schema if the specific import does not already exist in the schema.
func (s *Schema) AppendImport(imports string) {
	shouldAdd := true
	for _, si := range s.Imports {
		if si == imports {
			shouldAdd = false
			break
		}
	}

	if shouldAdd {
		s.Imports = append(s.Imports, imports)
	}

}

// String returns a string representation of a Schema.
func (s *Schema) String() string {
	buf := new(bytes.Buffer)
	buf.WriteString(fmt.Sprintf("syntax = \"%s\";\n", s.Syntax))
	buf.WriteString("\n")
	buf.WriteString(fmt.Sprintf("package %s;\n", s.Package))

	buf.WriteString("\n")
	buf.WriteString("// ------------------------------------ \n")
	buf.WriteString("// Rpc Func\n")
	buf.WriteString("// ------------------------------------ \n\n")

	funcTpl := "service " + s.ServiceName + "{ \n\n"
	for _, m := range s.Messages {
		funcTpl += "\t //-----------------------" + m.Comment + "----------------------- \n"
		funcTpl += "\t rpc AddList" + m.Name + "(AddList" + m.Name + "Request) returns (AddList" + m.Name + "Reply); \n"
		funcTpl += "\t rpc Edit" + m.Name + "(Edit" + m.Name + "Request) returns (Edit" + m.Name + "Reply); \n"
		funcTpl += "\t rpc Del" + m.Name + "(Del" + m.Name + "Request) returns (Del" + m.Name + "Reply); \n"
		funcTpl += "\t rpc GetPageList" + m.Name + "(GetPageList" + m.Name + "Request) returns (GetPageList" + m.Name + "Reply); \n"
	}
	funcTpl = funcTpl + "\n}"
	buf.WriteString(funcTpl)

	buf.WriteString("\n")
	buf.WriteString("// ------------------------------------ \n")
	buf.WriteString("// Messages\n")
	buf.WriteString("// ------------------------------------ \n\n")

	for _, m := range s.Messages {
		buf.WriteString("//--------------------------------" + m.Comment + "--------------------------------")
		buf.WriteString("\n")
		m.GenDefaultMessage(buf)
		m.GenRpcAddListReqRespMessage(buf)
		m.GenRpcEditReqMessage(buf)
		m.GenRpcDelReqMessage(buf)
		m.GenRpcGetPageListReqMessage(buf)
	}
	buf.WriteString("\n")

	if len(s.Enums) > 0 {
		buf.WriteString("// ------------------------------------ \n")
		buf.WriteString("// Enums\n")
		buf.WriteString("// ------------------------------------ \n\n")

		for _, e := range s.Enums {
			buf.WriteString(fmt.Sprintf("%s\n", e))
		}
	}

	return buf.String()
}

// Enum represents a protocol buffer enumerated type.
type Enum struct {
	Name    string
	Comment string
	Fields  []EnumField
}

// String returns a string representation of an Enum.
func (e *Enum) String() string {
	buf := new(bytes.Buffer)

	buf.WriteString(fmt.Sprintf("// %s \n", e.Comment))
	buf.WriteString(fmt.Sprintf("enum %s {\n", e.Name))

	for _, f := range e.Fields {
		buf.WriteString(fmt.Sprintf("%s%s;\n", indent, f))
	}

	buf.WriteString("}\n")

	return buf.String()
}

// AppendField appends an EnumField to an Enum.
func (e *Enum) AppendField(ef EnumField) error {
	for _, f := range e.Fields {
		if f.Tag() == ef.Tag() {
			return fmt.Errorf("tag `%d` is already in use by field `%s`", ef.Tag(), f.Name())
		}
	}

	e.Fields = append(e.Fields, ef)

	return nil
}

// EnumField represents a field in an enumerated type.
type EnumField struct {
	name string
	tag  int
}

// NewEnumField constructs an EnumField type.
func NewEnumField(name string, tag int) EnumField {
	name = strings.ToUpper(name)

	re := regexp.MustCompile(`([^\w]+)`)
	name = re.ReplaceAllString(name, "_")

	return EnumField{name, tag}
}

// String returns a string representation of an Enum.
func (ef EnumField) String() string {
	return fmt.Sprintf("%s = %d", ef.name, ef.tag)
}

// Name returns the name of the enum field.
func (ef EnumField) Name() string {
	return ef.name
}

// Tag returns the identifier tag of the enum field.
func (ef EnumField) Tag() int {
	return ef.tag
}

// newEnumFromStrings creates an enum from a name and a slice of strings that represent the names of each field.
func newEnumFromStrings(name, comment string, ss []string) (*Enum, error) {
	enum := &Enum{}
	enum.Name = name
	enum.Comment = comment

	for i, s := range ss {
		err := enum.AppendField(NewEnumField(s, i))
		if nil != err {
			return nil, err
		}
	}

	return enum, nil
}

// Service represents a protocol buffer service.
// TODO: Implement this in a schema.
type Service struct{}

// Message represents a protocol buffer message.
type Message struct {
	Name     string
	Comment  string
	Fields   []MessageField
	Style    string
	Messages []*Message
}

// GenDefaultMessage gen default message
func (m Message) GenDefaultMessage(buf *bytes.Buffer) {
	mOrginName := m.Name
	mOrginFields := m.Fields

	curFields := []MessageField{}
	var filedTag int
	for _, field := range m.Fields {
		if isInSlice([]string{"version", "del_state", "delete_time"}, field.Name) {
			continue
		}
		filedTag++
		field.tag = filedTag
		field.Name = stringx.From(field.Name).ToCamelWithStartLower()
		if m.Style == fieldStyleToSnake {
			field.Name = stringx.From(field.Name).ToSnake()
		}

		if field.Comment == "" {
			field.Comment = field.Name
		}
		curFields = append(curFields, field)
	}
	m.Fields = curFields
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields
}

// GenRpcAddReqRespMessage gen add req message
func (m Message) GenRpcAddListReqRespMessage(buf *bytes.Buffer) {
	mOrginName := m.Name
	mOrginFields := m.Fields

	//req
	m.Name = "AddList" + mOrginName + "Request"
	curField := MessageField{
		Typ:     "repeated " + mOrginName,
		tag:     1,
		Name:    stringx.From(mOrginName + "s").ToSnake(),
		Comment: mOrginName + " datas",
	}
	m.Fields = []MessageField{curField}
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields

	//resp
	m.Name = "AddList" + mOrginName + "Reply"
	m.Fields = []MessageField{
		MessageField{
			Typ:     "int32",
			tag:     1,
			Name:    "Code",
			Comment: "200:success,other:failure",
		},
		MessageField{
			Typ:     "string",
			tag:     2,
			Name:    "Msg",
			Comment: "failure cause",
		},
	}
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields

}

// GenRpcUpdateReqMessage gen add resp message
func (m Message) GenRpcEditReqMessage(buf *bytes.Buffer) {
	mOrginName := m.Name
	mOrginFields := m.Fields

	m.Name = "Edit" + mOrginName + "Request"
	curFields := []MessageField{}
	var filedTag int
	for _, field := range m.Fields {
		if isInSlice([]string{"create_time", "update_time", "version", "del_state", "delete_time"}, field.Name) {
			continue
		}
		filedTag++
		field.tag = filedTag
		field.Name = stringx.From(field.Name).ToCamelWithStartLower()
		if m.Style == fieldStyleToSnake {
			field.Name = stringx.From(field.Name).ToSnake()
		}
		if field.Comment == "" {
			field.Comment = field.Name
		}
		curFields = append(curFields, field)
	}
	m.Fields = curFields
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields

	//resp
	m.Name = "Edit" + mOrginName + "Reply"

	m.Fields = []MessageField{
		MessageField{
			Typ:     "int32",
			tag:     1,
			Name:    "Code",
			Comment: "200:success,other:failure",
		},
		MessageField{
			Typ:     "string",
			tag:     2,
			Name:    "Msg",
			Comment: "failure cause",
		},
	}
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields
}

// GenRpcDelReqMessage gen add resp message
func (m Message) GenRpcDelReqMessage(buf *bytes.Buffer) {
	mOrginName := m.Name
	mOrginFields := m.Fields

	m.Name = "Del" + mOrginName + "Request"
	m.Fields = []MessageField{
		{Name: "id", Typ: "int64", tag: 1, Comment: "id"},
	}
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields

	//resp
	m.Name = "Del" + mOrginName + "Reply"
	m.Fields = []MessageField{
		MessageField{
			Typ:     "int32",
			tag:     1,
			Name:    "Code",
			Comment: "200:success,other:failure",
		},
		MessageField{
			Typ:     "string",
			tag:     2,
			Name:    "Msg",
			Comment: "failure cause",
		},
	}
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields
}

// GenRpcGetPageListReqMessage gen add resp message
func (m Message) GenRpcGetPageListReqMessage(buf *bytes.Buffer) {
	mOrginName := m.Name
	mOrginFields := m.Fields

	m.Name = "GetPageList" + mOrginName + "Request"
	curFields := []MessageField{
		{Typ: "Where", Name: "Wheres", tag: 1, Comment: ""},
		{Typ: "Paging", Name: "Pagings", tag: 2, Comment: ""},
	}
	var filedTag = len(curFields)
	m.Messages = []*Message{
		{
			Name: "Where",
		},
		{
			Name: "Paging",
			Fields: []MessageField{
				{
					Typ: "int32", Name: "PageIndex", tag: 1,
				},
				{
					Typ: "int32", Name: "PageSize", tag: 2,
				},
			},
		},
	}
	for _, field := range m.Fields {
		if isInSlice([]string{"version", "del_state", "delete_time"}, field.Name) {
			continue
		}
		filedTag++
		field.tag = filedTag

		field.Name = stringx.From(field.Name).ToCamelWithStartLower()
		if m.Style == fieldStyleToSnake {
			field.Name = stringx.From(field.Name).ToSnake()
		}
		if field.Comment == "" {
			field.Comment = field.Name
		}
		m.Messages[0].Fields = append(m.Messages[0].Fields, field)
	}
	m.Fields = curFields
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields

	//resp
	firstWord := strings.ToLower(string(m.Name[0]))
	m.Name = "GetPageList" + mOrginName + "Reply"

	name := stringx.From(firstWord+mOrginName[1:]).ToCamelWithStartLower() + "s"
	comment := stringx.From(firstWord + mOrginName[1:]).ToCamelWithStartLower()
	if m.Style == fieldStyleToSnake {
		name = stringx.From(firstWord + mOrginName[1:]).ToSnake()
		comment = stringx.From(firstWord + mOrginName[1:]).ToSnake()
	}

	m.Fields = []MessageField{
		{Typ: "repeated " + mOrginName, Name: name, tag: 1, Comment: comment},
		{Typ: "int32", Name: "Total", tag: 2, Comment: "total"},
	}
	m.Messages = nil
	buf.WriteString(fmt.Sprintf("%s\n", m))

	//reset
	m.Name = mOrginName
	m.Fields = mOrginFields
}

// String returns a string representation of a Message.
func (m Message) String() string {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("message %s {\n", m.Name))
	for _, f := range m.Fields {
		buf.WriteString(fmt.Sprintf("%s%s; //%s\n", indent, f, f.Comment))
	}
	if m.Messages != nil && len(m.Messages) > 0 {
		for _, f := range m.Messages {
			buf.WriteString(fmt.Sprintf("%smessage %s {\n", indent, f.Name))
			for _, f1 := range f.Fields {
				buf.WriteString(fmt.Sprintf("%s%s%s; //%s\n", indent, indent, f1, f1.Comment))
			}
			buf.WriteString(fmt.Sprintf("%s}\n", indent))
		}
	}
	buf.WriteString("}\n")

	return buf.String()
}

// AppendField appends a message field to a message. If the tag of the message field is in use, an error will be returned.
func (m *Message) AppendField(mf MessageField) error {
	for _, f := range m.Fields {
		if f.Tag() == mf.Tag() {
			return fmt.Errorf("tag `%d` is already in use by field `%s`", mf.Tag(), f.Name)
		}
	}

	m.Fields = append(m.Fields, mf)

	return nil
}

// MessageField represents the field of a message.
type MessageField struct {
	Typ     string
	Name    string
	tag     int
	Comment string
}

// NewMessageField creates a new message field.
func NewMessageField(typ, name string, tag int, comment string) MessageField {
	return MessageField{typ, name, tag, comment}
}

// Tag returns the unique numbered tag of the message field.
func (f MessageField) Tag() int {
	return f.tag
}

// String returns a string representation of a message field.
func (f MessageField) String() string {
	return fmt.Sprintf("%s %s = %d", f.Typ, f.Name, f.tag)
}

// Column represents a database column.
type Column struct {
	Style                  string
	TableName              string
	TableComment           string
	ColumnName             string
	IsNullable             string
	DataType               string
	CharacterMaximumLength sql.NullInt64
	NumericPrecision       sql.NullInt64
	NumericScale           sql.NullInt64
	ColumnType             string
	ColumnComment          string
}

// Table represents a database table.
type Table struct {
	TableName  string
	ColumnName string
}

// parseColumn parses a column and inserts the relevant fields in the Message. If an enumerated type is encountered, an Enum will
// be added to the Schema. Returns an error if an incompatible protobuf data type cannot be found for the database column type.
func parseColumn(s *Schema, msg *Message, col Column) error {
	typ := strings.ToLower(col.DataType)
	var fieldType string

	switch typ {
	case "char", "varchar", "text", "longtext", "mediumtext", "tinytext":
		fieldType = "string"
	case "enum", "set":
		// Parse c.ColumnType to get the enum list
		enumList := regexp.MustCompile(`[enum|set]\((.+?)\)`).FindStringSubmatch(col.ColumnType)
		enums := strings.FieldsFunc(enumList[1], func(c rune) bool {
			cs := string(c)
			return "," == cs || "'" == cs
		})

		enumName := inflect.Singularize(snaker.SnakeToCamel(col.TableName)) + snaker.SnakeToCamel(col.ColumnName)
		enum, err := newEnumFromStrings(enumName, col.ColumnComment, enums)
		if nil != err {
			return err
		}

		s.Enums = append(s.Enums, enum)

		fieldType = enumName
	case "blob", "mediumblob", "longblob", "varbinary", "binary":
		fieldType = "bytes"
	case "date", "time", "datetime", "timestamp":
		//s.AppendImport("google/protobuf/timestamp.proto")
		fieldType = "int64"
	case "bool", "bit":
		fieldType = "bool"
	case "tinyint", "smallint", "int", "mediumint", "bigint":
		fieldType = "int64"
	case "float", "decimal", "double":
		fieldType = "double"
	case "json":
		fieldType = "string"
	}

	if "" == fieldType {
		return fmt.Errorf("no compatible protobuf type found for `%s`. column: `%s`.`%s`", col.DataType, col.TableName, col.ColumnName)
	}

	field := NewMessageField(fieldType, col.ColumnName, len(msg.Fields)+1, col.ColumnComment)

	err := msg.AppendField(field)
	if nil != err {
		return err
	}

	return nil
}

func isInSlice(slice []string, s string) bool {
	for i, _ := range slice {
		if slice[i] == s {
			return true
		}
	}
	return false
}

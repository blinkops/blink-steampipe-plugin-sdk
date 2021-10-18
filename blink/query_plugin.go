package blink

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	blinkPlugin "github.com/blinkops/blink-sdk/plugin"
	"github.com/blinkops/blink-sdk/plugin/connections"
	"github.com/turbot/steampipe-plugin-sdk/connection"
	"github.com/turbot/steampipe-plugin-sdk/grpc/proto"
	steamPlugin "github.com/turbot/steampipe-plugin-sdk/plugin"
	"google.golang.org/grpc/metadata"
	"time"
)

const ActionContextKey = "actionContext"

type QueryPlugin struct {
	Description     blinkPlugin.Description
	SteamPipePlugin *steamPlugin.Plugin
	TestCredentialsFunc func(ctx *blinkPlugin.ActionContext) (*blinkPlugin.CredentialsValidationResponse, error)
}


func (q *QueryPlugin) Describe() blinkPlugin.Description {
	return q.Description
}

func (q *QueryPlugin) GetActions() []blinkPlugin.Action {
	var actions []blinkPlugin.Action
	for name, table := range q.SteamPipePlugin.TableMap {
		output := &blinkPlugin.Output{Name: name}
		for _, column := range table.Columns {
			field := blinkPlugin.Field{
				Name: column.Name,
				Type: convertFieldType(column.Type),
			}
			output.Fields = append(output.Fields, field)
		}
		action := blinkPlugin.Action{
			Name:        name,
			Description: table.Description,
			Enabled:     true,
			Output:      output,
		}
		actions = append(actions, action)
	}
	return actions
}

func convertFieldType(columnType proto.ColumnType) string {
	//goland:noinspection ALL
	switch columnType {
	case proto.ColumnType_STRING:
		return "string"
	case proto.ColumnType_BOOL:
		return "bool"
	case proto.ColumnType_INT:
		return "int"
	case proto.ColumnType_DOUBLE:
		return "double"
	case proto.ColumnType_JSON:
		return "json"
	// Deprecated: ColumnType_DATETIME is deprecated. Instead, use ColumnType_TIMESTAMP
	case proto.ColumnType_DATETIME:
		return "datetime"
	case proto.ColumnType_IPADDR:
		return "ipaddress"
	case proto.ColumnType_CIDR:
		return "cidr"
	case proto.ColumnType_TIMESTAMP:
		return "timestamp"
	}
	return "unknown"
}

type ResultStream struct {
	rows []map[string]string
}

func (r *ResultStream) Send(response *proto.ExecuteResponse) error {
	row := map[string]string{}
	for name, col := range response.Row.GetColumns() {
		row[name] = stringValue(col)
	}
	r.rows = append(r.rows, row)
	return nil
}

func stringValue(column *proto.Column) string {
	switch x := column.Value.(type) {
	case *proto.Column_StringValue:
		return x.StringValue
	case *proto.Column_NullValue:
		return ""
	case *proto.Column_DoubleValue:
		return fmt.Sprintf("%f", x.DoubleValue)
	case *proto.Column_IntValue:
		return fmt.Sprintf("%d", x.IntValue)
	case *proto.Column_BoolValue:
		return fmt.Sprintf("%t", x.BoolValue)
	case *proto.Column_JsonValue:
		return string(x.JsonValue)
	case *proto.Column_TimestampValue:
		return x.TimestampValue.String()
	case *proto.Column_IpAddrValue:
		return x.IpAddrValue
	case *proto.Column_CidrRangeValue:
		return x.CidrRangeValue
	}
	return ""
}

func (r *ResultStream) SetHeader(metadata.MD) error {
	panic("SetHeader: should not be called")
}

func (r *ResultStream) SendHeader(metadata.MD) error {
	panic("SendHeader: should not be called")
}

func (r *ResultStream) SetTrailer(metadata.MD) {
	panic("SetTrailer: should not be called")
}

func (r *ResultStream) Context() context.Context {
	panic("Context: should not be called")
}

func (r *ResultStream) SendMsg(interface{}) error {
	panic("SendMsg: should not be called")
}

func (r *ResultStream) RecvMsg(interface{}) error {
	panic("RecvMsg: should not be called")
}

func (q *QueryPlugin) ExecuteAction(actionContext *blinkPlugin.ActionContext, request *blinkPlugin.ExecuteActionRequest) (response *blinkPlugin.ExecuteActionResponse, err error) {

	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()

	queryContext, err := convertQueryContext(request)
	if err != nil {
		return nil, err
	}

	tableName := request.Name
	err = q.addColumnNames(queryContext, tableName)
	if err != nil {
		return nil, err
	}

	stream := &ResultStream{}
	executeRequest := &proto.ExecuteRequest{Table: tableName, QueryContext: queryContext}

	// Create context with actionContext and timeout for plugins to use
	ctx := context.WithValue(context.TODO(), ActionContextKey, actionContext)
	ctx = context.WithValue(ctx, "timeout", time.Duration(request.Timeout)*time.Second)
	ctx = addConnectionsToContext(ctx, actionContext.GetAllConnections())

	err = q.SteamPipePlugin.Execute0(ctx, executeRequest, stream)
	if err != nil {
		return nil, err
	}

	return &blinkPlugin.ExecuteActionResponse{
		Rows: stream.rows,
	}, nil

}

func addConnectionsToContext(ctx context.Context, connections map[string]*connections.ConnectionInstance) context.Context {
	js, err := json.Marshal(connections)
	if err != nil {
		return ctx
	}
	md := fmt.Sprintf("%x", md5.Sum(js))
	return context.WithValue(ctx, connection.CacheConnectionKey, md)
}

func convertQueryContext(request *blinkPlugin.ExecuteActionRequest) (*proto.QueryContext, error) {
	pqc := &proto.QueryContext{Quals: map[string]*proto.Quals{}}
	var qc QueryContext
	queryContext, ok := request.Parameters["query.ctx"]
	if !ok {
		return pqc, nil
	}

	err := json.Unmarshal([]byte(queryContext), &qc)
	if err != nil {
		return nil, err
	}

	for name, constraintList := range qc.Constraints {
		quals := &proto.Quals{}
		for _, constraint := range constraintList.Constraints {
			qualValue := &proto.QualValue{}
			qualValue.Value = &proto.QualValue_StringValue{StringValue: constraint.Expression}
			qual := &proto.Qual{
				FieldName: name,
				Operator:  &proto.Qual_StringValue{StringValue: convertOperator(constraint.Operator)},
				Value:     qualValue,
			}
			quals.Quals = append(quals.Quals, qual)
		}
		pqc.Quals[name] = quals
	}
	return pqc, nil
}

func convertOperator(operator Operator) string {
	switch operator {
	case OperatorEquals:
		return "="
	case OperatorGreaterThan:
		return ">"
	case OperatorLessThanOrEquals:
		return "<="
	case OperatorLessThan:
		return "<"
	case OperatorGreaterThanOrEquals:
		return ">="
	case OperatorMatch: // what?
		return "like"
	case OperatorLike:
		return "like"
	case OperatorGlob: // what?
		return "like"
	case OperatorRegexp: // what?
		return "like"
	case OperatorUnique: // what?
		return "unique"
	}
	return "unsupported"
}

// QueryContext contains the constraints from the WHERE clause of the query,
// that can optionally be used to optimize the table generation. Note that the
// osquery SQLite engine will perform the filtering with these constraints, so
// it is not mandatory that they be used in table generation.
type QueryContext struct {
	// Constraints is a map from column name to the details of the
	// constraints on that column.
	Constraints map[string]ConstraintList
}

// ConstraintList contains the details of the constraints for the given column.
type ConstraintList struct {
	Affinity    string
	Constraints []Constraint
}

// Constraint contains both an operator and an expression that are applied as
// constraints in the query.
type Constraint struct {
	Operator   Operator
	Expression string
}

// Operator is an enum of the osquery operators.
type Operator int

// The following operators are defined in osquery tables.h.
const (
	OperatorEquals              Operator = 2
	OperatorGreaterThan                  = 4
	OperatorLessThanOrEquals             = 8
	OperatorLessThan                     = 16
	OperatorGreaterThanOrEquals          = 32
	OperatorMatch                        = 64
	OperatorLike                         = 65
	OperatorGlob                         = 66
	OperatorRegexp                       = 67
	OperatorUnique                       = 1
)

func (q *QueryPlugin) TestCredentials(conn map[string]*connections.ConnectionInstance) (*blinkPlugin.CredentialsValidationResponse, error) {
	if q.TestCredentialsFunc == nil {
		return nil, errors.New("no TestCredentials function found")
	}

	return q.TestCredentialsFunc(blinkPlugin.NewActionContext(nil, conn))
}

func (q *QueryPlugin) addColumnNames(queryContext *proto.QueryContext, tableName string) error {
	table, ok := q.SteamPipePlugin.TableMap[tableName]
	if !ok {
		return errors.New("table not found: " + tableName)
	}
	for _, column := range table.Columns {
		queryContext.Columns = append(queryContext.Columns, column.Name)
	}
	return nil
}

package query

import (
	"fmt"
	"strings"
)

// QueryBuilder helps build SQL queries with common filters
type QueryBuilder struct {
	baseQuery      string
	whereConditions []string
	orderByClauses []string
	limitClause    string
	offsetClause   string
	args           []interface{}
	argPos         int
}

// NewQueryBuilder creates a new query builder instance
func NewQueryBuilder(baseQuery string) *QueryBuilder {
	return &QueryBuilder{
		baseQuery:       baseQuery,
		whereConditions: []string{},
		orderByClauses:  []string{},
		args:            []interface{}{},
		argPos:          1,
	}
}

// AddSoftDeleteFilter adds deleted_at IS NULL filter
func (qb *QueryBuilder) AddSoftDeleteFilter() *QueryBuilder {
	qb.whereConditions = append(qb.whereConditions, "deleted_at IS NULL")
	return qb
}

// AddFilter adds a generic filter with field, operator, and value
func (qb *QueryBuilder) AddFilter(field string, operator string, value interface{}) *QueryBuilder {
	qb.whereConditions = append(qb.whereConditions, fmt.Sprintf("%s %s $%d", field, operator, qb.argPos))
	qb.args = append(qb.args, value)
	qb.argPos++
	return qb
}

// AddSearchFilter adds ILIKE search filter for multiple fields
func (qb *QueryBuilder) AddSearchFilter(search string, fields ...string) *QueryBuilder {
	if search == "" || len(fields) == 0 {
		return qb
	}

	conditions := []string{}
	for _, field := range fields {
		conditions = append(conditions, fmt.Sprintf("%s ILIKE $%d", field, qb.argPos))
	}

	qb.whereConditions = append(qb.whereConditions, fmt.Sprintf("(%s)", strings.Join(conditions, " OR ")))
	qb.args = append(qb.args, "%"+search+"%")
	qb.argPos++
	return qb
}

// AddIsActiveFilter adds is_active filter
// fieldName parameter allows specifying the full field name with table prefix (e.g., "cu.is_active")
// If empty, defaults to "is_active"
func (qb *QueryBuilder) AddIsActiveFilter(isActive *bool, fieldName ...string) *QueryBuilder {
	if isActive == nil {
		return qb
	}

	field := "is_active"
	if len(fieldName) > 0 && fieldName[0] != "" {
		field = fieldName[0]
	}

	qb.whereConditions = append(qb.whereConditions, fmt.Sprintf("%s = $%d", field, qb.argPos))
	qb.args = append(qb.args, *isActive)
	qb.argPos++
	return qb
}

// AddCustomCondition adds a custom WHERE condition with arguments
func (qb *QueryBuilder) AddCustomCondition(condition string, args ...interface{}) *QueryBuilder {
	if condition == "" {
		return qb
	}

	// Replace placeholders in condition with actual positions
	replacedCondition := condition
	for i := range args {
		placeholder := fmt.Sprintf("$%d", i+1)
		actualPlaceholder := fmt.Sprintf("$%d", qb.argPos)
		replacedCondition = strings.Replace(replacedCondition, placeholder, actualPlaceholder, 1)
		qb.args = append(qb.args, args[i])
		qb.argPos++
	}

	qb.whereConditions = append(qb.whereConditions, replacedCondition)
	return qb
}

// AddOrderBy adds ORDER BY clause
func (qb *QueryBuilder) AddOrderBy(field string, direction string) *QueryBuilder {
	if field == "" {
		return qb
	}

	dir := strings.ToUpper(direction)
	if dir != "ASC" && dir != "DESC" {
		dir = "ASC"
	}

	qb.orderByClauses = append(qb.orderByClauses, fmt.Sprintf("%s %s", field, dir))
	return qb
}

// AddPagination adds LIMIT and OFFSET
func (qb *QueryBuilder) AddPagination(limit, offset int) *QueryBuilder {
	if limit > 0 {
		qb.limitClause = fmt.Sprintf("LIMIT $%d", qb.argPos)
		qb.args = append(qb.args, limit)
		qb.argPos++
	}

	if offset > 0 {
		qb.offsetClause = fmt.Sprintf("OFFSET $%d", qb.argPos)
		qb.args = append(qb.args, offset)
		qb.argPos++
	}

	return qb
}

// Build constructs the final query and returns it with arguments
func (qb *QueryBuilder) Build() (string, []interface{}) {
	query := qb.baseQuery

	// Add WHERE clause
	if len(qb.whereConditions) > 0 {
		query += " WHERE " + strings.Join(qb.whereConditions, " AND ")
	}

	// Add ORDER BY clause
	if len(qb.orderByClauses) > 0 {
		query += " ORDER BY " + strings.Join(qb.orderByClauses, ", ")
	}

	// Add LIMIT
	if qb.limitClause != "" {
		query += " " + qb.limitClause
	}

	// Add OFFSET
	if qb.offsetClause != "" {
		query += " " + qb.offsetClause
	}

	return query, qb.args
}

// GetNextArgPos returns the next argument position (useful for manual additions)
func (qb *QueryBuilder) GetNextArgPos() int {
	return qb.argPos
}

// GetArgs returns current arguments
func (qb *QueryBuilder) GetArgs() []interface{} {
	return qb.args
}

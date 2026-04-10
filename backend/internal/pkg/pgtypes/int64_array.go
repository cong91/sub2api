package pgtypes

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"

	"entgo.io/ent/schema/field"
	"github.com/lib/pq"
)

// Int64Array binds canonically to Postgres bigint[].
type Int64Array []int64

func (a Int64Array) Value() (driver.Value, error) {
	return pq.Array([]int64(a)).Value()
}

func (a *Int64Array) Scan(src any) error {
	if a == nil {
		return fmt.Errorf("pgtypes.Int64Array: nil receiver")
	}
	if src == nil {
		*a = Int64Array{}
		return nil
	}

	var values []int64
	if err := pq.Array(&values).Scan(src); err == nil {
		*a = Int64Array(values)
		return nil
	}

	switch v := src.(type) {
	case []byte:
		parsed, err := parseArrayLiteral(string(v))
		if err != nil {
			return err
		}
		*a = Int64Array(parsed)
		return nil
	case string:
		parsed, err := parseArrayLiteral(v)
		if err != nil {
			return err
		}
		*a = Int64Array(parsed)
		return nil
	default:
		return fmt.Errorf("pgtypes.Int64Array: unsupported scan type %T", src)
	}
}

func parseArrayLiteral(s string) ([]int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return []int64{}, nil
	}
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return nil, fmt.Errorf("pgtypes.Int64Array: unsupported array literal %q", s)
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return []int64{}, nil
	}
	parts := strings.Split(inner, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, `"`))
		if part == "" || strings.EqualFold(part, "NULL") {
			continue
		}
		v, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("pgtypes.Int64Array: parse %q: %w", part, err)
		}
		out = append(out, v)
	}
	return out, nil
}

var Int64ArrayValueScanner = field.ValueScannerFunc[Int64Array, *sql.NullString]{
	V: func(v Int64Array) (driver.Value, error) {
		return v.Value()
	},
	S: func(ns *sql.NullString) (Int64Array, error) {
		if ns == nil || !ns.Valid || strings.TrimSpace(ns.String) == "" {
			return Int64Array{}, nil
		}
		parsed, err := parseArrayLiteral(ns.String)
		if err != nil {
			return nil, err
		}
		return Int64Array(parsed), nil
	},
}

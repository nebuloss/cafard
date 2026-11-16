package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// key is one scalar setting, found by walking Config's yaml tags.
type key struct {
	name  string // "http_listen", "otlp.max_body_mib"
	index []int  // reflect field path from Config
	typ   reflect.Type
}

// flagName is the key as a flag: dots and underscores become dashes.
func (k key) flagName() string {
	return strings.NewReplacer(".", "-", "_", "-").Replace(k.name)
}

// envName is the key as an environment variable.
func (k key) envName() string {
	return "CAFARD_" + strings.ToUpper(strings.ReplaceAll(k.name, ".", "_"))
}

var (
	durationType        = reflect.TypeFor[time.Duration]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// scalarKeys lists every key that may be set by a flag or the environment.
func scalarKeys() []key {
	var keys []key
	walkType(reflect.TypeFor[Config](), "", nil, &keys)
	return keys
}

func walkType(t reflect.Type, prefix string, index []int, keys *[]key) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := yamlName(f)
		if name == "" {
			continue
		}
		if prefix != "" {
			name = prefix + "." + name
		}
		path := append(append([]int(nil), index...), i)
		switch {
		case isScalar(f.Type):
			*keys = append(*keys, key{name: name, index: path, typ: f.Type})
		case f.Type.Kind() == reflect.Struct:
			walkType(f.Type, name, path, keys)
		}
	}
}

func yamlName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

func isScalar(t reflect.Type) bool {
	if reflect.PointerTo(t).Implements(textUnmarshalerType) || t == durationType {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// setString parses s into v, which must be addressable and scalar.
func setString(v reflect.Value, s string) error {
	if u, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return u.UnmarshalText([]byte(s))
	}
	if v.Type() == durationType {
		d, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		v.SetInt(int64(d))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("%q is not true or false", s)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an integer", s)
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a non-negative integer", s)
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", s)
		}
		v.SetFloat(n)
	default:
		return fmt.Errorf("type %s cannot be set from text", v.Type())
	}
	return nil
}

// applyDefaults calls SetDefaults on every nested struct that has one.
func applyDefaults(c *Config) {
	_ = walkStructs(reflect.ValueOf(c).Elem(), "", func(_ string, v reflect.Value) error {
		if d, ok := v.Addr().Interface().(Defaulter); ok {
			d.SetDefaults()
		}
		return nil
	})
}

// validateStructs calls Validate on every nested struct that has one and
// names the key of the first that fails.
func validateStructs(v reflect.Value) error {
	return walkStructs(v, "", func(name string, v reflect.Value) error {
		if val, ok := v.Addr().Interface().(Validator); ok {
			if err := val.Validate(); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		return nil
	})
}

// walkStructs visits every nested struct field with its dotted key, a
// parent before its fields, and stops at the first error.
func walkStructs(v reflect.Value, prefix string, visit func(name string, v reflect.Value) error) error {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() || f.Type.Kind() != reflect.Struct || isScalar(f.Type) {
			continue
		}
		name := yamlName(f)
		if name == "" {
			continue
		}
		if prefix != "" {
			name = prefix + "." + name
		}
		if err := visit(name, v.Field(i)); err != nil {
			return err
		}
		if err := walkStructs(v.Field(i), name, visit); err != nil {
			return err
		}
	}
	return nil
}

// Redacted is a copy with every field tagged `secret:"true"` replaced by a
// placeholder, for printing.
func (c *Config) Redacted() Config {
	out := *c
	redact(reflect.ValueOf(&out).Elem())
	return out
}

const redactedText = "<redacted>"

// redact walks structs only: no secret lives in a list or a map.
func redact(v reflect.Value) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		if f.Tag.Get("secret") == "true" && fv.Kind() == reflect.String {
			if fv.String() != "" {
				fv.SetString(redactedText)
			}
			continue
		}
		redact(fv)
	}
}

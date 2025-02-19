package config

import (
	"bytes"
	"fmt"
	"github.com/Masterminds/sprig"
	"github.com/e14914c0-6759-480d-be89-66b7b7676451/BitterJohn/common"
	"github.com/e14914c0-6759-480d-be89-66b7b7676451/BitterJohn/pkg/log"
	"github.com/spf13/viper"
	"reflect"
	"strconv"
	"strings"
	"text/template"
)

var (
	ErrRequired               = fmt.Errorf("required")
	ErrMutualReference        = fmt.Errorf("mutual reference or invalid value")
	ErrOverlayHierarchicalKey = fmt.Errorf("overlay hierarchical key")
)

type Binder struct {
	viper     *viper.Viper
	toResolve map[string]string
	resolved  map[string]interface{}
}

func NewBinder(viper *viper.Viper) *Binder {
	return &Binder{
		viper:     viper,
		toResolve: make(map[string]string),
		resolved:  make(map[string]interface{}),
	}
}

func (b *Binder) Bind(iface interface{}) error {
	if err := b.bind(iface); err != nil {
		return err
	}
	for len(b.toResolve) > 0 {
		var changed bool
		for key, expr := range b.toResolve {
			ok, err := b.bindKey(key, expr)
			if err != nil {
				return err
			}
			if ok {
				changed = true
				if err := SetValueHierarchicalMap(b.resolved, key, b.viper.Get(key)); err != nil {
					return fmt.Errorf("%w: %v", err, key)
				}
				delete(b.toResolve, key)
			}
		}
		if !changed {
			return fmt.Errorf("%v: %w", strings.Join(common.MustMapKeys(b.toResolve), ", "), ErrMutualReference)
		}
	}
	return nil
}

func (b *Binder) bind(iface interface{}, parts ...string) error {
	// https://github.com/spf13/viper/issues/188
	ifv := reflect.ValueOf(iface)
	ift := reflect.TypeOf(iface)
nextField:
	for i := 0; i < ift.NumField(); i++ {
		v := ifv.Field(i)
		t := ift.Field(i)
		tv, ok := t.Tag.Lookup("mapstructure")
		if !ok {
			continue
		}
		fields := strings.Split(tv, ",")
		tv = fields[0]
		switch v.Kind() {
		case reflect.Struct:
			if err := b.bind(v.Interface(), append(parts, tv)...); err != nil {
				return err
			}
		default:
			key := strings.Join(append(parts, tv), ".")
			if b.viper.Get(key) == nil {
				if defaultValue, ok := t.Tag.Lookup("default"); ok {
					ok, err := b.bindKey(key, defaultValue)
					if err != nil {
						return err
					}
					if !ok {
						b.toResolve[key] = defaultValue
						continue nextField
					}
				} else if _, ok := t.Tag.Lookup("required"); ok {
					if desc, ok := t.Tag.Lookup("desc"); ok {
						key += " (" + desc + ")"
					}
					return fmt.Errorf("%w: %v", ErrRequired, key)
				} else if len(fields) == 1 || fields[1] != "omitempty" {
					// write an empty value
					b.viper.Set(key, "")
				}
			}
			if err := SetValueHierarchicalMap(b.resolved, key, b.viper.Get(key)); err != nil {
				return fmt.Errorf("%w: %v", err, key)
			}
		}
	}
	return nil
}

func (b *Binder) bindKey(key string, expr string) (ok bool, err error) {
	//	support `toml:"port" default:"{{with $arr := split \":\" .john.listen}}{{$arr._1}}{{end}}"`
	tmpl, err := template.New(key).Funcs(sprig.TxtFuncMap()).Parse(expr)
	if err != nil {
		return false, fmt.Errorf("failed to parse default value of key %v: %w", key, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, b.resolved); err != nil {
		log.Warn("%v", err)
		if strings.Contains(err.Error(), "invalid value") {
			return false, nil
		}
		return false, fmt.Errorf("failed to parse default value of key %v: %w", key, err)
	}
	if strings.Contains(buf.String(), "<no value>") {
		log.Warn("%v", buf.String())
		return false, nil
	}
	b.viper.Set(key, buf.String())
	return true, nil
}

func SetValueHierarchicalMap(m map[string]interface{}, key string, val interface{}) error {
	keys := strings.Split(key, ".")
	lastKey := keys[len(keys)-1]
	keys = keys[:len(keys)-1]
	p := &m
	for _, key := range keys {
		if v, ok := (*p)[key]; ok {
			vv, ok := v.(map[string]interface{})
			if !ok {
				return ErrOverlayHierarchicalKey
			}
			p = &vv
		} else {
			(*p)[key] = make(map[string]interface{})
			vv := (*p)[key].(map[string]interface{})
			p = &vv
		}
	}
	(*p)[lastKey] = val
	return nil
}

func SetValueHierarchicalStruct(m interface{}, key string, val string) error {
	keys := strings.Split(key, ".")
	ifv := reflect.Indirect(reflect.ValueOf(m))
	ift := ifv.Type()
	for _, key := range keys {
		found := false
		for i := 0; i < ifv.NumField(); i++ {
			name, ok := ift.Field(i).Tag.Lookup("mapstructure")
			if ok && name == key {
				found = true
				ifv = ifv.Field(i)
				ift = ifv.Type()
				break
			}
		}
		if !found {
			return fmt.Errorf("unexpected key: %v", key)
		}
	}
	if !FuzzyDecode(ifv.Addr().Interface(), val) {
		return fmt.Errorf("type does not match: type \"%v\" and value \"%v\"", ifv.Kind(), val)
	}
	return nil
}

func FuzzyDecode(to interface{}, val string) bool {
	v := reflect.Indirect(reflect.ValueOf(to))
	switch v.Kind() {
	case reflect.Int:
		i, err := strconv.ParseInt(val, 10, strconv.IntSize)
		if err != nil {
			return false
		}
		v.SetInt(i)
	case reflect.Int8:
		i, err := strconv.ParseInt(val, 10, 8)
		if err != nil {
			return false
		}
		v.SetInt(i)
	case reflect.Int16:
		i, err := strconv.ParseInt(val, 10, 16)
		if err != nil {
			return false
		}
		v.SetInt(i)
	case reflect.Int32:
		i, err := strconv.ParseInt(val, 10, 32)
		if err != nil {
			return false
		}
		v.SetInt(i)
	case reflect.Int64:
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return false
		}
		v.SetInt(i)
	case reflect.Uint:
		i, err := strconv.ParseUint(val, 10, strconv.IntSize)
		if err != nil {
			return false
		}
		v.SetUint(i)
	case reflect.Uint8:
		i, err := strconv.ParseUint(val, 10, 8)
		if err != nil {
			return false
		}
		v.SetUint(i)
	case reflect.Uint16:
		i, err := strconv.ParseUint(val, 10, 16)
		if err != nil {
			return false
		}
		v.SetUint(i)
	case reflect.Uint32:
		i, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return false
		}
		v.SetUint(i)
	case reflect.Uint64:
		i, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return false
		}
		v.SetUint(i)
	case reflect.Bool:
		if val == "true" || val == "1" {
			v.SetBool(true)
		} else if val == "false" || val == "0" {
			v.SetBool(false)
		} else {
			return false
		}
	case reflect.String:
		v.SetString(val)
	}
	return true
}

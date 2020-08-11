package crdgo

import (
	"encoding/json"
	"path"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dave/jennifer/jen"
	"github.com/zoumo/golib/reflection"
)

var (
	imports = &importsList{
		byAlias: make(map[string]string),
		byPath:  make(map[string]string),
	}
)

// importsList keeps track of required imports, automatically assigning aliases
// to import statement.
type importsList struct {
	byPath  map[string]string
	byAlias map[string]string
}

// NeedImport marks that the given package is needed in the list of imports,
// returning the ident (import alias) that should be used to reference the package.
func (l *importsList) NeedImport(importPath string) string {
	// we get an actual path from Package, which might include venddored
	// packages if running on a package in vendor.
	if ind := strings.LastIndex(importPath, "/vendor/"); ind != -1 {
		importPath = importPath[ind+8: /* len("/vendor/") */]
	}

	// check to see if we've already assigned an alias, and just return that.
	alias, exists := l.byPath[importPath]
	if exists {
		return alias
	}

	// otherwise, calculate an import alias by joining path parts till we get something unique
	restPath, nextWord := path.Split(importPath)

	for otherPath, exists := "", true; exists && otherPath != importPath; otherPath, exists = l.byAlias[alias] {
		if restPath == "" {
			// do something else to disambiguate if we're run out of parts and
			// still have duplicates, somehow
			alias += "x"
		}

		// can't have a first digit, per Go identifier rules, so just skip them
		for firstRune, runeLen := utf8.DecodeRuneInString(nextWord); unicode.IsDigit(firstRune); firstRune, runeLen = utf8.DecodeRuneInString(nextWord) {
			nextWord = nextWord[runeLen:]
		}

		// make a valid identifier by replacing "bad" characters with underscores
		nextWord = strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				return r
			}
			return '_'
		}, nextWord)

		alias = nextWord + alias
		if len(restPath) > 0 {
			restPath, nextWord = path.Split(restPath[:len(restPath)-1] /* chop off final slash */)
		}
	}

	l.byPath[importPath] = alias
	l.byAlias[alias] = importPath
	return alias
}

func (l *importsList) Qual(pkg, name string) *jen.Statement {
	l.NeedImport(pkg)
	return jen.Qual(pkg, name)
}

func (l *importsList) ImportAlias() map[string]string {
	return l.byPath
}

func GenerateValue(i interface{}) *jen.Statement {
	return generateValue(reflect.ValueOf(i), false)
}

func generateValue(v reflect.Value, omitType bool) *jen.Statement {
	t := v.Type()

	if reflection.IsLiteralType(t) {
		return generateLiteralValue(v)
	}

	ts := generateType(t)
	if omitType {
		ts = &jen.Statement{}
	}

	switch t.Kind() {
	case reflect.Ptr:
		return generatePtrValue(v, omitType)
	case reflect.Slice, reflect.Array:
		return ts.ValuesFunc(func(j *jen.Group) {
			for i := 0; i < v.Len(); i++ {
				j.Line().Add(generateValue(v.Index(i), true))
			}
			if v.Len() > 0 {
				j.Line()
			}
		})
	case reflect.Map:
		return ts.Values(jen.DictFunc(func(d jen.Dict) {
			iter := v.MapRange()
			for iter.Next() {
				kv := iter.Key()
				vv := iter.Value()
				d[generateValue(kv, false)] = generateValue(vv, true)
			}

		}))
	case reflect.Struct:
		return generateStructValue(v, false, omitType)
	case reflect.Chan, reflect.Func:
		// skip
	}
	return nil

}

func generatePtrValue(v reflect.Value, omitType bool) *jen.Statement {
	t := v.Type()
	if t.Kind() != reflect.Ptr {
		return nil
	}
	te := t.Elem()
	if reflection.IsLiteralType(te) {
		return generatePtrLiteralValue(v.Elem())
	} else if te.Kind() == reflect.Struct {
		return generateStructValue(v.Elem(), true, omitType)
	}

	if omitType {
		generateValue(v.Elem(), true)
	}

	return jen.Op("&").Add(generateValue(v.Elem(), false))
}

func generateType(t reflect.Type) *jen.Statement {
	switch t.Kind() {
	case reflect.Ptr:
		return jen.Op("*").Add(generateType(t.Elem()))
	case reflect.Slice:
		return jen.Index().Add(generateType(t.Elem()))
	case reflect.Array:
		return jen.Index(jen.Lit(t.Len())).Add(generateType(t.Elem()))
	case reflect.Map:
		return jen.Map(generateType(t.Key())).Add(generateType(t.Elem()))
	case reflect.Struct:
		return imports.Qual(t.PkgPath(), t.Name())
	case reflect.Chan:
		return jen.Chan().Add(generateType(t.Elem()))
	case reflect.Func:
		// skip
	default:
		if reflection.IsCustomType(t) {
			return imports.Qual(t.PkgPath(), t.Name())
		}
		return jen.Id(t.Kind().String())
	}
	return nil
}

func generatePtrLiteralValue(v reflect.Value) *jen.Statement {
	t := v.Type()
	if !reflection.IsLiteralType(t) {
		return nil
	}
	value := generateBuiltinLiteralValue(v)
	// convert to pointer
	p := imports.Qual("github.com/zoumo/golib/pointer", Capitalize(t.Kind().String())).Call(value)
	if reflection.IsCustomType(t) {
		// convert custom pointer
		p = jen.Parens(jen.Op("*").Qual(t.PkgPath(), t.Name())).Parens(p)
	}
	return p
}

func generateLiteralValue(v reflect.Value) *jen.Statement {
	t := v.Type()
	if !reflection.IsLiteralType(t) {
		return nil
	}
	var ts *jen.Statement
	ts = generateBuiltinLiteralValue(v)
	if reflection.IsCustomType(t) {
		ts = imports.Qual(t.PkgPath(), t.Name()).Parens(ts)
	}
	return ts
}

func generateBuiltinLiteralValue(v reflect.Value) *jen.Statement {
	var value interface{}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			value = true
		} else {
			value = false
		}
	case reflect.Int:
		value = int(v.Int())
	case reflect.Int8:
		value = int8(v.Int())
	case reflect.Int16:
		value = int16(v.Int())
	case reflect.Int32:
		value = int32(v.Int())
	case reflect.Int64:
		value = v.Int()
	case reflect.Uint:
		value = uint(v.Uint())
	case reflect.Uint8:
		value = uint8(v.Uint())
	case reflect.Uint16:
		value = uint16(v.Uint())
	case reflect.Uint32:
		value = uint32(v.Uint())
	case reflect.Uint64:
		value = v.Uint()
	case reflect.Float32:
		value = float32(v.Float())
	case reflect.Float64:
		value = v.Float()
	case reflect.String:
		value = v.String()
	case reflect.Uintptr:
		value = uintptr(v.Uint())
	}

	return jen.Lit(value)
}

func generateStructValue(v reflect.Value, ptrResult bool, omitType bool) *jen.Statement {
	t := v.Type()
	if t.Kind() != reflect.Struct {
		return nil
	}
	if reflection.HasUnexportedField(t) {
		j := generateStructWithUnexportedField(v, ptrResult)
		if j != nil {
			return j
		}
	}

	var ts *jen.Statement
	if reflection.IsAnonymousStruct(t) {
		ts = jen.StructFunc(func(g *jen.Group) {
			for i := 0; i < t.NumField(); i++ {
				field := t.Field(i)
				g.Id(field.Name).Add(generateType(field.Type))
			}
		})
	} else {
		ts = imports.Qual(t.PkgPath(), t.Name())
	}

	if ptrResult {
		ts = jen.Op("&").Add(ts)
	}

	if omitType {
		ts = &jen.Statement{}
	}

	return ts.Values(jen.DictFunc(func(d jen.Dict) {
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)

			if reflection.IsUnexportedField(field) ||
				reflection.IsAnonymousStruct(field.Type) {
				continue
			}

			if !v.Field(i).IsZero() {
				// omit zero value
				d[jen.Id(field.Name)] = generateValue(v.Field(i), false)
			}
		}
	}))
}

func generateStructWithUnexportedField(v reflect.Value, ptrResult bool) *jen.Statement {
	t := v.Type()
	if t.Kind() != reflect.Struct {
		return nil
	}
	structS := imports.Qual(t.PkgPath(), t.Name())
	ptrT := reflect.PtrTo(t)

	marshaler := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	unmarshaler := reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	if (t.Implements(marshaler) || ptrT.Implements(marshaler)) &&
		(t.Implements(unmarshaler) || ptrT.Implements(unmarshaler)) {
		// func() *T | T {
		f := jen.Func().Params()
		if ptrResult {
			f.Op("*")
		}
		f.Add(structS.Clone()).BlockFunc(func(g *jen.Group) {
			jsonBytes, _ := json.Marshal(v.Interface())
			// jsonBytes := "string"
			g.Id("jsonStr").Op(":=").Lit(string(jsonBytes))
			// var obj T
			g.Var().Id("obj").Add(structS.Clone())
			// json.Unmarshal([]byte(jsonStr), &obj)
			g.Qual("encoding/json", "Unmarshal").Call(jen.Index().Byte().Parens(jen.Id("jsonStr")), jen.Op("&").Id("obj"))
			// return obj | return &obj
			if ptrResult {
				g.Return(jen.Op("&").Id("obj"))
			} else {
				g.Return(jen.Id("obj"))
			}
		}).Call()
		// }()
		return f
	}
	return nil
}

func Capitalize(str string) string {
	if len(str) < 1 {
		return ""
	}
	strArry := []rune(str)
	if strArry[0] >= 97 && strArry[0] <= 122 {
		strArry[0] -= 32
	}
	return string(strArry)
}

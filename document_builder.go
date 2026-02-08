package be_indexer

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// DocumentBuilder 用于从任意 struct 构建 Document
type DocumentBuilder struct {
	docIDField string // 标记哪个字段是 DocID
}

// NewDocumentBuilder 创建新的 DocumentBuilder
func NewDocumentBuilder() *DocumentBuilder {
	return &DocumentBuilder{
		docIDField: "id",
	}
}

// SetDocIDField 设置 DocID 字段名（默认为 "id"）
func (b *DocumentBuilder) SetDocIDField(fieldName string) *DocumentBuilder {
	b.docIDField = fieldName
	return b
}

// Build 从任意 struct 构建 Document
// struct 可以包含以下 tag：
//   - be_indexer:"fieldName" - 基础字段
//   - be_indexer:"fieldName,exclude" - 排除模式
//   - be_indexer:"fieldName,exclude,eq" - 带表达式类型
//
// 同一个 struct 内的所有字段组合成一个 Conjunction（AND 关系）
// 嵌套的 struct slice 每个元素生成一个独立的 Conjunction（OR 关系）
// DocID 字段标记：be_indexer:"doc_id"
func (b *DocumentBuilder) Build(v interface{}) (*Document, error) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil, fmt.Errorf("expected struct, got %s", val.Kind())
	}

	typ := val.Type()
	var docID DocID

	// 收集所有字段表达式到一个 Conjunction
	mainConj := NewConjunction()
	// 收集嵌套 slice 生成的额外 Conjunction
	var extraCons []*Conjunction

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		fieldVal := val.Field(i)

		// 获取 be_indexer tag
		tag := field.Tag.Get("be_indexer")

		// 处理 DocID 字段
		if tag == "doc_id" || (field.Name == b.docIDField && tag == "") {
			docID = b.extractDocID(fieldVal)
			continue
		}

		// 处理嵌套 struct - 无条件展开其子字段
		if fieldVal.Kind() == reflect.Struct {
			// 跳过 time.Time 等特殊类型
			if fieldVal.Type().String() == "time.Time" {
				continue
			}
			// 单个嵌套 struct - 展开其字段到主 Conjunction
			if err := b.addNestedFields(mainConj, fieldVal); err != nil {
				return nil, err
			}
			continue
		}

		// 处理 slice
		if fieldVal.Kind() == reflect.Slice {
			// 如果没有 tag，跳过
			if tag == "" || tag == "-" {
				continue
			}

			// 解析 tag
			parsed := parseFieldTag(tag)
			if parsed.isDocID {
				continue
			}

			// 检查元素类型
			if fieldVal.Len() > 0 {
				elemType := fieldVal.Type().Elem()
				if elemType.Kind() == reflect.Struct {
					// struct slice - 每个元素生成一个 Conjunction
					for j := 0; j < fieldVal.Len(); j++ {
						elem := fieldVal.Index(j)
						nestedCons, err := b.buildNestedConjunction(elem)
						if err != nil {
							return nil, err
						}
						extraCons = append(extraCons, nestedCons...)
					}
				} else {
					// 基本类型 slice - 添加到主 Conjunction
					if err := b.addSliceField(mainConj, parsed.fieldName, fieldVal, parsed); err != nil {
						return nil, err
					}
				}
			}
			continue
		}

		// 处理基本类型字段（需要 be_indexer tag）
		if tag == "" || tag == "-" {
			continue
		}

		parsed := parseFieldTag(tag)
		if parsed.isDocID {
			continue
		}
		if err := b.addField(mainConj, parsed.fieldName, fieldVal, parsed); err != nil {
			return nil, err
		}
	}

	if docID == 0 {
		return nil, fmt.Errorf("doc ID not found, please mark a field with be_indexer:\"doc_id\"")
	}

	doc := NewDocument(docID)

	// 如果主 Conjunction 有表达式，添加到文档
	if mainConj.CalcConjSize() > 0 {
		extraCons = append([]*Conjunction{mainConj}, extraCons...)
	}

	// 添加所有 Conjunction
	if len(extraCons) > 0 {
		doc.AddConjunction(extraCons...)
	}

	return doc, nil
}

// BuildSlice 批量从 struct slice 构建 Documents
func (b *DocumentBuilder) BuildSlice(v interface{}) ([]*Document, error) {
	val := reflect.ValueOf(v)
	if val.Kind() != reflect.Slice {
		return nil, fmt.Errorf("expected slice, got %s", val.Kind())
	}

	docs := make([]*Document, 0, val.Len())
	for i := 0; i < val.Len(); i++ {
		elem := val.Index(i)
		if elem.Kind() == reflect.Ptr {
			if elem.IsNil() {
				continue
			}
			elem = elem.Elem()
		}
		doc, err := b.Build(elem.Interface())
		if err != nil {
			return nil, fmt.Errorf("failed to build document at index %d: %w", i, err)
		}
		if doc != nil {
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

// fieldTag 解析后的 tag 配置
type fieldTag struct {
	fieldName string
	exclude   bool
	exprType  string // eq, gt, lt, between
	isDocID   bool
}

func parseFieldTag(tag string) fieldTag {
	parts := strings.Split(tag, ",")
	t := fieldTag{
		fieldName: parts[0],
		exclude:   false,
		exprType:  "eq",
		isDocID:   false,
	}

	// 如果第一个参数是 "doc_id" 或 "exclude"，则特殊处理
	if len(parts) == 1 {
		if strings.ToLower(parts[0]) == "doc_id" {
			t.isDocID = true
		}
		return t
	}

	// 处理第二个及之后的参数
	for i := 1; i < len(parts); i++ {
		p := strings.ToLower(parts[i])
		switch p {
		case "doc_id":
			t.isDocID = true
		case "exclude":
			t.exclude = true
		default:
			if p == "gt" || p == "lt" || p == "between" {
				t.exprType = p
			}
		}
	}

	return t
}

// addField 将基本类型字段添加到 Conjunction
func (b *DocumentBuilder) addField(conj *Conjunction, fieldName string, fieldVal reflect.Value, tag fieldTag) error {
	if fieldVal.IsZero() {
		return nil
	}

	switch tag.exprType {
	case "eq":
		if tag.exclude {
			conj.NotIn(BEField(fieldName), b.valueToInterface(fieldVal))
		} else {
			conj.In(BEField(fieldName), b.valueToInterface(fieldVal))
		}
	case "gt":
		conj.GreaterThan(BEField(fieldName), fieldVal.Int())
	case "lt":
		conj.LessThan(BEField(fieldName), fieldVal.Int())
	case "between":
		conj.Between(BEField(fieldName), fieldVal.Int(), fieldVal.Int())
	default:
		return fmt.Errorf("unknown exprType: %s", tag.exprType)
	}
	return nil
}

// addSliceField 将切片字段添加到 Conjunction
func (b *DocumentBuilder) addSliceField(conj *Conjunction, fieldName string, fieldVal reflect.Value, tag fieldTag) error {
	if fieldVal.Len() == 0 {
		return nil
	}

	if tag.exclude {
		if intVals := b.sliceToInt64(fieldVal); len(intVals) > 0 {
			conj.NotIn(BEField(fieldName), intVals)
		} else if strVals := b.sliceToString(fieldVal); len(strVals) > 0 {
			conj.NotIn(BEField(fieldName), strVals)
		} else {
			conj.NotIn(BEField(fieldName), b.sliceToInterface(fieldVal))
		}
	} else {
		if intVals := b.sliceToInt64(fieldVal); len(intVals) > 0 {
			conj.In(BEField(fieldName), intVals)
		} else if strVals := b.sliceToString(fieldVal); len(strVals) > 0 {
			conj.In(BEField(fieldName), strVals)
		} else {
			conj.In(BEField(fieldName), b.sliceToInterface(fieldVal))
		}
	}
	return nil
}

// addNestedFields 将嵌套 struct 的字段展开到 Conjunction
func (b *DocumentBuilder) addNestedFields(conj *Conjunction, fieldVal reflect.Value) error {
	typ := fieldVal.Type()
	for i := 0; i < typ.NumField(); i++ {
		nestedField := typ.Field(i)
		nestedFieldVal := fieldVal.Field(i)

		tag := nestedField.Tag.Get("be_indexer")
		if tag == "" || tag == "-" {
			continue
		}

		parsed := parseFieldTag(tag)
		if parsed.isDocID {
			continue // 嵌套 struct 中的 DocID 字段被忽略
		}

		if err := b.addField(conj, parsed.fieldName, nestedFieldVal, parsed); err != nil {
			return err
		}
	}
	return nil
}

// buildNestedConjunction 为嵌套 struct 创建独立的 Conjunction
func (b *DocumentBuilder) buildNestedConjunction(fieldVal reflect.Value) ([]*Conjunction, error) {
	conj := NewConjunction()
	if err := b.addNestedFields(conj, fieldVal); err != nil {
		return nil, err
	}
	if conj.CalcConjSize() == 0 {
		return nil, nil
	}
	return []*Conjunction{conj}, nil
}

// sliceToInt64 将 slice 转换为 []int64
func (b *DocumentBuilder) sliceToInt64(slice reflect.Value) []int64 {
	if slice.Len() == 0 {
		return nil
	}

	result := make([]int64, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		switch v := slice.Index(i).Interface().(type) {
		case int64:
			result = append(result, v)
		case int:
			result = append(result, int64(v))
		case int32:
			result = append(result, int64(v))
		default:
			return nil
		}
	}
	return result
}

// sliceToString 将 slice 转换为 []string
func (b *DocumentBuilder) sliceToString(slice reflect.Value) []string {
	if slice.Len() == 0 {
		return nil
	}

	result := make([]string, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		switch v := slice.Index(i).Interface().(type) {
		case string:
			result = append(result, v)
		default:
			return nil
		}
	}
	return result
}

// sliceToInterface 将 slice 转换为 []interface{}
func (b *DocumentBuilder) sliceToInterface(slice reflect.Value) []interface{} {
	if slice.Len() == 0 {
		return nil
	}

	result := make([]interface{}, 0, slice.Len())
	for i := 0; i < slice.Len(); i++ {
		result = append(result, slice.Index(i).Interface())
	}
	return result
}

// valueToInterface 将单个值转换为 interface{}
func (b *DocumentBuilder) valueToInterface(fieldVal reflect.Value) interface{} {
	switch fieldVal.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fieldVal.Int()
	case reflect.String:
		return fieldVal.String()
	case reflect.Bool:
		return fieldVal.Bool()
	case reflect.Float32, reflect.Float64:
		return fieldVal.Float()
	default:
		return fieldVal.Interface()
	}
}

// extractDocID 从反射值中提取 DocID
func (b *DocumentBuilder) extractDocID(fieldVal reflect.Value) DocID {
	switch fieldVal.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return DocID(fieldVal.Int())
	case reflect.String:
		if v, err := strconv.ParseInt(fieldVal.String(), 10, 64); err == nil {
			return DocID(v)
		}
	}
	return 0
}

// MustBuild 类似 Build，但会在错误时 panic
func (b *DocumentBuilder) MustBuild(v interface{}) *Document {
	doc, err := b.Build(v)
	if err != nil {
		panic(err)
	}
	return doc
}

// MustBuildSlice 类似 BuildSlice，但会在错误时 panic
func (b *DocumentBuilder) MustBuildSlice(v interface{}) []*Document {
	docs, err := b.BuildSlice(v)
	if err != nil {
		panic(err)
	}
	return docs
}

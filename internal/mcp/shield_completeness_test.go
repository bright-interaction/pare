// SPDX-License-Identifier: AGPL-3.0-or-later
package mcp

import (
	"reflect"
	"strings"
	"testing"
)

// safeFields are non-PII json field names allowed to be untagged on an MCP
// result struct. Every other untagged string field fails this guard: give it a
// shield tag if it can hold an identity, or add it here if it is genuinely safe.
// This is the check the rest of the estate never had (partial Shield coverage
// is the recurring leak); it forces a conscious decision per field.
var safeFields = map[string]bool{
	"number": true, "total_kr": true, "net_kr": true, "vat_kr": true,
	"due_date": true, "date": true, "status": true, "account": true,
	"account_name": true, "description": true, "series": true, "filename": true,
	"sie_base64": true, "verification_id": true, "result_kr": true,
	"output_vat_kr": true, "input_vat_kr": true, "moms_to_pay_kr": true,
	"unpaid_total_kr": true, "total_net_kr": true,
	"box_05_net_sales_kr": true, "box_10_output_25_kr": true, "box_11_output_12_kr": true,
	"box_12_output_6_kr": true, "box_30_reverse_charge_output_kr": true,
	"box_39_eu_services_kr": true, "box_48_input_kr": true, "box_49_to_pay_kr": true,
	"at": true, "actor": true, "action": true, "target": true, "detail": true,
	"total": true, "currency": true, "total_sek": true,
}

func TestShieldCompleteness(t *testing.T) {
	s := &Server{tools: map[string]tool{}}
	s.register()
	if len(s.tools) == 0 {
		t.Fatal("no tools registered")
	}
	for name, tl := range s.tools {
		checkType(t, name, reflect.TypeOf(tl.proto))
	}
}

func checkType(t *testing.T, tool string, rt reflect.Type) {
	for rt != nil && (rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array) {
		rt = rt.Elem()
	}
	if rt == nil || rt.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		switch f.Type.Kind() {
		case reflect.String:
			tagged := strings.HasPrefix(f.Tag.Get("shield"), "tokenize")
			name := jsonName(f)
			if !tagged && !safeFields[name] {
				t.Errorf("tool %s: field %q (json %q) is neither shield-tagged nor in safeFields; it could leak PII to the LLM", tool, f.Name, name)
			}
		case reflect.Struct, reflect.Slice, reflect.Array, reflect.Pointer:
			checkType(t, tool, f.Type)
		}
	}
}

func jsonName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return strings.ToLower(f.Name)
	}
	if i := strings.IndexByte(tag, ','); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

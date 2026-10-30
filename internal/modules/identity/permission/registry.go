// Package permission owns the three-part codes used by identity seeds and authorization.
package permission

import (
	"errors"
	"regexp"
	"strings"
)

// Codes for the minimum M1a navigation and role acceptance dataset.
const (
	SystemUserList  = "system:user:list"
	SystemMenuList  = "system:menu:list"
	TradeOrderList  = "trade:order:list"
	CRMCustomerList = "crm:customer:list"
	CRMInquiryList  = "crm:inquiry:list"
)

var segment = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Definition is a permission catalog entry, independent from persistence identifiers.
type Definition struct{ Code, Name, Module, Type string }

// All returns an owned copy of the minimum permission catalog.
func All() []Definition {
	return []Definition{
		{SystemUserList, "查看用户", "system", "api"},
		{SystemMenuList, "查看菜单", "system", "api"},
		{TradeOrderList, "查看订单", "trade", "api"},
		{CRMCustomerList, "查看客户", "crm", "api"},
		{CRMInquiryList, "查看询盘", "crm", "api"},
	}
}

// Validate enforces three lowercase segments and a matching module namespace.
func Validate(module, code string) error {
	parts := strings.Split(code, ":")
	if len(parts) != 3 || parts[0] != module {
		return errors.New("permission code must have three segments matching its module")
	}
	for _, part := range parts {
		if !segment.MatchString(part) {
			return errors.New("permission segments must be lowercase identifiers")
		}
	}
	return nil
}

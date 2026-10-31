// Package permission owns the three-part codes used by identity seeds and authorization.
package permission

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Codes cover the current system module and the published M1a compatibility entries.
const (
	SystemUserList    = "system:user:list"
	SystemUserCreate  = "system:user:create"
	SystemUserUpdate  = "system:user:update"
	SystemUserDelete  = "system:user:delete"
	SystemUserReset   = "system:user:reset"
	SystemUserGrant   = "system:user:grant"
	SystemRoleList    = "system:role:list"
	SystemRoleCreate  = "system:role:create"
	SystemRoleUpdate  = "system:role:update"
	SystemRoleDelete  = "system:role:delete"
	SystemRoleGrant   = "system:role:grant"
	SystemMenuList    = "system:menu:list"
	SystemMenuCreate  = "system:menu:create"
	SystemMenuUpdate  = "system:menu:update"
	SystemMenuDelete  = "system:menu:delete"
	SystemSessionList = "system:session:list"
	SystemSessionKick = "system:session:kick"
	TradeOrderList    = "trade:order:list"
	CRMCustomerList   = "crm:customer:list"
	CRMInquiryList    = "crm:inquiry:list"
)

var segment = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Definition is a permission catalog entry, independent from persistence identifiers.
type Definition struct {
	Code   string `json:"code"`
	Name   string `json:"name"`
	Module string `json:"module"`
	Type   string `json:"type"`
}

// All returns an owned copy of the permission catalog.
func All() []Definition {
	return []Definition{
		{SystemUserList, "查看用户", "system", "api"},
		{SystemUserCreate, "创建用户", "system", "api"},
		{SystemUserUpdate, "更新用户", "system", "api"},
		{SystemUserDelete, "删除用户", "system", "api"},
		{SystemUserReset, "重置密码", "system", "api"},
		{SystemUserGrant, "分配用户角色", "system", "api"},
		{SystemRoleList, "查看角色", "system", "api"},
		{SystemRoleCreate, "创建角色", "system", "api"},
		{SystemRoleUpdate, "更新角色", "system", "api"},
		{SystemRoleDelete, "删除角色", "system", "api"},
		{SystemRoleGrant, "角色授权", "system", "api"},
		{SystemMenuList, "查看菜单", "system", "api"},
		{SystemMenuCreate, "创建菜单", "system", "api"},
		{SystemMenuUpdate, "更新菜单", "system", "api"},
		{SystemMenuDelete, "删除菜单", "system", "api"},
		{SystemSessionList, "查看会话", "system", "api"},
		{SystemSessionKick, "强制下线", "system", "api"},
		{TradeOrderList, "查看订单", "trade", "api"},
		{CRMCustomerList, "查看客户", "crm", "api"},
		{CRMInquiryList, "查看询盘", "crm", "api"},
	}
}

// Registered rejects syntactically valid codes absent from the authoritative dictionary.
func Registered(code string) error {
	for _, item := range All() {
		if item.Code == code {
			return Validate(item.Module, code)
		}
	}
	return errors.New("permission is not registered")
}

// Export writes a deterministic JSON catalog, sorted by complete permission code.
func Export(out io.Writer) error {
	items := All()
	sort.Slice(items, func(i, j int) bool { return items[i].Code < items[j].Code })
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(items)
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

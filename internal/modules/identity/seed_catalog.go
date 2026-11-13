package identity

import (
	"encoding/json"

	"github.com/omwk/go-admin-api/internal/modules/identity/model"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
	"github.com/omwk/go-admin-api/internal/modules/identity/repo"
)

func seedMenus() []repo.SeedMenu {
	return []repo.SeedMenu{
		seedMenu("系统", "dir", "/system", "", "", "", 1),
		seedMenu("用户", "menu", "/system/users", "/system", "system/user/index", permission.SystemUserList, 1),
		seedMenu("菜单", "menu", "/system/menus", "/system", "system/menu/index", permission.SystemMenuList, 2),
		seedMenu("角色", "menu", "/system/roles", "/system", "system/role/index", permission.SystemRoleList, 3),
		seedMenu("部门", "menu", "/system/depts", "/system", "system/dept/index", permission.SystemUserList, 4),
		seedMenu("通知中心", "menu", "/system/notifications", "/system", "system/notification/index", permission.SystemNotifyList, 5),
		seedMenu("通知渠道", "menu", "/system/notifications/channels", "/system", "system/notification/channels", permission.SystemNotifyManage, 6),
		seedMenu("标记已读", "button", "/system/notifications/read", "/system/notifications", "", permission.SystemNotifyRead, 1),
		seedMenu("新建部门", "button", "/system/depts/create", "/system/depts", "", permission.SystemDeptCreate, 1),
		seedMenu("编辑部门", "button", "/system/depts/update", "/system/depts", "", permission.SystemDeptUpdate, 2),
		seedMenu("删除部门", "button", "/system/depts/delete", "/system/depts", "", permission.SystemDeptDelete, 3),
		seedMenu("交易", "dir", "/trade", "", "", "", 2),
		seedMenu("订单", "menu", "/trade/orders", "/trade", "trade/order/index", permission.TradeOrderList, 1),
		seedMenu("查看订单", "button", "/trade/orders/view", "/trade/orders", "", permission.TradeOrderList, 1),
		seedMenu("客户", "dir", "/crm", "", "", "", 3),
		seedMenu("客户列表", "menu", "/crm/customers", "/crm", "crm/customer/index", permission.CRMCustomerList, 1),
		seedMenu("询盘", "menu", "/crm/inquiries", "/crm", "crm/inquiry/index", permission.CRMInquiryList, 2),
		seedMenu("审计日志", "menu", "/system/audit-logs", "/system", "system/audit-log/index", permission.SystemAuditList, 7),
		seedMenu("登录历史", "menu", "/system/login-logs", "/system", "system/login-log/index", permission.SystemAuditList, 8),
	}
}

func seedMenu(name, kind, path, parent, component, code string, order int) repo.SeedMenu {
	row := model.Menu{Name: name, Type: kind, Path: &path, Sort: order, Status: "active", Visible: true, Meta: json.RawMessage("{}")}
	if component != "" {
		row.Component = &component
	}
	if code != "" {
		row.PermissionCode = &code
	}
	return repo.SeedMenu{Row: row, ParentPath: parent}
}

func seedRoles() []repo.SeedRole {
	allCodes, allPaths := []string{}, []string{}
	for _, item := range permission.All() {
		allCodes = append(allCodes, item.Code)
	}
	for _, menu := range seedMenus() {
		allPaths = append(allPaths, *menu.Row.Path)
	}
	return []repo.SeedRole{
		{Row: model.Role{Code: SuperAdminRoleCode, Name: "超级管理员", DataScope: "all", Status: "active"}, Permissions: allCodes, MenuPaths: allPaths},
		{Row: model.Role{Code: OperatorRoleCode, Name: "运营", DataScope: "self", Status: "active"}, Permissions: []string{permission.TradeOrderList}, MenuPaths: []string{"/trade", "/trade/orders", "/trade/orders/view"}},
		{Row: model.Role{Code: SalesRoleCode, Name: "销售", DataScope: "self", Status: "active"}, Permissions: []string{permission.CRMCustomerList, permission.CRMInquiryList}, MenuPaths: []string{"/crm", "/crm/customers", "/crm/inquiries"}},
	}
}

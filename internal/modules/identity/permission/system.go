package permission

// System configuration and dictionary management capabilities are independent of notification-channel writes.
const (
	SystemSettingList   = "system:setting:list"
	SystemSettingUpdate = "system:setting:update"
	SystemDictList      = "system:dict:list"
	SystemDictCreate    = "system:dict:create"
	SystemDictUpdate    = "system:dict:update"
	SystemDictDelete    = "system:dict:delete"
	SystemNotifyList    = "system:notify:list"
	SystemNotifyManage  = "system:notify:manage"
	SystemNotifyRead    = "system:notify:read"
)

func systemMetadataPermissions() []Definition {
	return []Definition{
		{SystemSettingList, "查看系统配置", "system", "api"}, {SystemSettingUpdate, "更新系统配置", "system", "api"},
		{SystemDictList, "查看数据字典", "system", "api"}, {SystemDictCreate, "创建数据字典", "system", "api"},
		{SystemDictUpdate, "更新数据字典", "system", "api"}, {SystemDictDelete, "删除数据字典", "system", "api"},
		{SystemNotifyList, "查看通知", "system", "api"}, {SystemNotifyManage, "维护通知渠道", "system", "api"},
		{SystemNotifyRead, "标记通知已读", "system", "api"},
	}
}

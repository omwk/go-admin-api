package permission

// System configuration and dictionary management capabilities are independent of notification-channel writes.
const (
	SystemSettingList   = "system:setting:list"
	SystemSettingUpdate = "system:setting:update"
	SystemDictList      = "system:dict:list"
	SystemDictCreate    = "system:dict:create"
	SystemDictUpdate    = "system:dict:update"
	SystemDictDelete    = "system:dict:delete"
)

func systemMetadataPermissions() []Definition {
	return []Definition{
		{SystemSettingList, "查看系统配置", "system", "api"}, {SystemSettingUpdate, "更新系统配置", "system", "api"},
		{SystemDictList, "查看数据字典", "system", "api"}, {SystemDictCreate, "创建数据字典", "system", "api"},
		{SystemDictUpdate, "更新数据字典", "system", "api"}, {SystemDictDelete, "删除数据字典", "system", "api"},
	}
}

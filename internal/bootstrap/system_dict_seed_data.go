package bootstrap

type dictSeedItem struct {
	label, value string
	sort         int
	color        string
}

type dictSeed struct {
	kind, name string
	items      []dictSeedItem
}

func dictionarySeeds() []dictSeed {
	return []dictSeed{
		{"user_status", "用户状态", []dictSeedItem{
			{"正常", "active", 1, "green"}, {"停用", "disabled", 2, "default"}, {"锁定", "locked", 3, "red"},
		}},
		{"sync_run_status", "同步运行状态", []dictSeedItem{
			{"运行中", "running", 1, "blue"}, {"成功", "success", 2, "green"}, {"部分成功", "partial", 3, "orange"},
			{"失败", "failed", 4, "red"}, {"已取消", "cancelled", 5, "default"},
		}},
		{"order_status", "订单状态", []dictSeedItem{
			{"待付款", "pending_payment", 1, "orange"}, {"待发货", "pending_shipment", 2, "blue"},
			{"已发货", "shipped", 3, "cyan"}, {"已完成", "completed", 4, "green"}, {"已关闭", "closed", 5, "default"},
			{"退款中", "refunding", 6, "orange"}, {"已退款", "refunded", 7, "purple"}, {"未知", "unknown", 8, "default"},
		}},
		{"product_status", "商品状态", []dictSeedItem{
			{"草稿", "draft", 1, "default"}, {"启用", "active", 2, "green"}, {"已归档", "archived", 3, "orange"},
		}},
		{"tenant_status", "租户状态", []dictSeedItem{
			{"启用", "active", 1, "green"}, {"已停用", "suspended", 2, "red"},
		}},
		{"audit_result", "审计结果", []dictSeedItem{
			{"成功", "success", 1, "green"}, {"失败", "failure", 2, "red"},
		}},
		{"enable_status", "启用状态", []dictSeedItem{
			{"启用", "active", 1, "green"}, {"停用", "disabled", 2, "default"},
		}},
		{"delivery_status", "投递状态", []dictSeedItem{
			{"待发送", "pending", 1, "orange"}, {"已发送", "sent", 2, "green"},
			{"失败", "failed", 3, "red"}, {"已跳过", "skipped", 4, "default"},
		}},
		{"invite_status", "邀请状态", []dictSeedItem{
			{"有效", "active", 1, "green"}, {"已过期", "expired", 2, "orange"},
			{"已撤销", "revoked", 3, "red"}, {"已使用", "used", 4, "default"},
		}},
	}
}

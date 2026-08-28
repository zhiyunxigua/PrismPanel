# PrismPanel PlayerData 邮件系统接入

## 目标

- 增加 PlayerData 地址、Token 和 `features.mail` 配置，邮件功能默认关闭。
- 由面板服务端代理调用 PlayerData 邮件发送接口，Token 不暴露给浏览器。
- 在功能启用且具备 `mail.send` 权限时提供后台邮件发送页面。

## 验收标准

- 关闭功能时接口拒绝发送且前端不显示导航入口。
- 支持系统/管理员邮件、全体/指定玩家、标题正文和 `item_key` 附件数量。
- PlayerData 错误转换为面板错误，审计详情不包含 Token、正文或完整收件人数据。
- Go 测试和前端构建通过。

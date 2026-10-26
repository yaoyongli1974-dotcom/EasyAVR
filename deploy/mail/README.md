# 企业邮箱 DNS 记录（Zoho Mail 免费版）

`easyavr.cn` 上的 `avr@easyavr.cn` 用于官网联系入口，方案为 Zoho Mail Forever Free（1 个域名 / 5 个账号 / 支持 IMAP-SMTP 主动发信）。

## 前置条件

- 域名 `easyavr.cn` 已完成**实名认证**（`.cn` 强制，未通过会被 Registry 置 `serverHold`，届时 MX 生效也会失效）。
- 能修改 `easyavr.cn` 的 DNS 解析记录。
- 确认该域名**当前没有**其他邮件服务在用（切换 MX 会中断原邮箱）。

## 需要在 DNS 服务商添加的记录

`<...>` 部分以 Zoho 控制台实际给出的值为准，尤其是 DKIM 公钥与验证串。

| 用途 | 类型 | 主机记录 | 值 | 优先级 |
|---|---|---|---|---|
| 域名归属验证 | TXT | `@` | `zoho-verification=<控制台给出>` | — |
| 收信 | MX | `@` | `mx.zoho.com` | 10 |
| 收信 | MX | `@` | `mx2.zoho.com` | 20 |
| 收信 | MX | `@` | `mx3.zoho.com` | 50 |
| SPF | TXT | `@` | `v=spf1 include:zoho.com ~all` | — |
| DKIM | TXT | `zmail._domainkey` | `<控制台生成的公钥>` | — |
| DMARC | TXT | `_dmarc` | `v=DMARC1; p=none; rua=mailto:avr@easyavr.cn` | — |

注意：

- `@` 上如果已有 SPF 记录，**不要新增第二条**，把 `include:zoho.com` 合并进现有的 `v=spf1`。
- MX 与 SPF 都落在 `@` 上，与该域名现有的 A/CNAME（官网解析）不冲突，可以共存。
- 邮件服务商若要求 CNAME 形式的 `zmail._domainkey`，按控制台给的来，不要照抄本表的 TXT。

## 验证

```bash
dig +short MX easyavr.cn        # 期望 3 条 zoho MX
dig +short TXT easyavr.cn       # 期望含 v=spf1 ... 与 zoho-verification
dig +short TXT zmail._domainkey.easyavr.cn
dig +short TXT _dmarc.easyavr.cn
```

DNS 有缓存延迟，改完通常 10 分钟到 1 小时生效，`dig` 未生效前不要判定配置失败。最后从外部邮箱发一封邮件到 `avr@easyavr.cn` 做端到端确认。

## 客户端参数

| 协议 | 地址 | 端口 |
|---|---|---|
| IMAP | `imap.zoho.com` | 993（SSL） |
| SMTP | `smtp.zoho.com` | 465（SSL） |

## 边界

- 免费版有**每日发信限额**，只适合客户往来。平台自身的告警邮件走 `internal/notify` 里的 SMTP 配置，应单独接发信服务（如阿里邮件推送、Resend），**不要复用这个邮箱**，否则量一上来就被限流，且退信会影响域名信誉。
- 若将来需要更多账号或更大附件，升级 Zoho 付费版即可，DNS 记录无需改动。
- 阿里云企业邮箱的**免费版已不对新用户开放**（`wanwang.aliyun.com/mail/freemail/` 现已跳转到付费产品页），不要按旧教程去找免费入口。

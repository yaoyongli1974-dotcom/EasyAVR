# EasyAVR 官网（site/）

纯静态宣传落地页：零构建、零外部依赖。`index.html` 双击可直接打开，或交给任意静态服务器托管。

## 目录结构

```
site/
  index.html         页面（唯一入口）
  assets/styles.css  全部样式（无 CSS 框架）
  assets/main.js     移动端导航 / 代码块复制 / 滚动入场（原生 JS）
```

## 本地预览

```bash
python3 -m http.server -d site 8080
```

打开 <http://localhost:8080>。

## 部署

正式域名 `easyavr.cn`（服务器在海外，无备案，勿加 ICP 备案号）。

三个站点职责：

| 域名 | 内容 |
|---|---|
| `easyavr.cn` | 本目录静态页面 |
| `app.easyavr.cn` | 反代到 EasyAVR 后端 `:18000`（生产构建时后端托管 `web/dist`，SPA 走 `index.html` 兜底） |
| `demo.easyavr.cn` | 反代到一个常驻演示实例（与 app 同栈即可） |

nginx 参考配置：

```nginx
server {
    listen 80;
    server_name easyavr.cn www.easyavr.cn;
    root /opt/easyavr/site;
    index index.html;

    location / { try_files $uri $uri/ =404; }
}

server {
    listen 80;
    server_name app.easyavr.cn demo.easyavr.cn;

    location / {
        proxy_pass http://127.0.0.1:18000;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        # WebRTC / HLS 需要的转发设置
        proxy_http_version 1.1;
        proxy_set_header Upgrade    $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }
}
```

演示实例：把 `app.easyavr.cn` 那段 server 块单独再起一份（后端跑在别的端口 / 数据目录），`proxy_pass` 指向它即可。

TLS：域名解析到位后用 certbot 签发：

```bash
sudo certbot --nginx -d easyavr.cn -d app.easyavr.cn -d demo.easyavr.cn
```

certbot 会自动改写上面的 server 块为 443 + 强制跳转。

## 后续待补内容

- **产品截图**：`index.html` 中 `#shots` 一节有 3 个虚线占位框（搜索 `产品截图待补`），替换为真实截图时放到 `site/assets/shots/`（如 `multiscreen.png`、`event-map.png`、`diagnosis.png`），把 `.shot-box` 内的占位换成 `<img>`，并保留 figcaption 文案。注意保持 16:9。
- **GitHub 仓库地址**：页脚「参与」一节有 `TODO: GitHub 仓库地址` 注释，确定后替换 `href="#"`。
- **联系邮箱**：页脚「参与」一节有 `TODO: 联系邮箱` 注释，确定后改为 `mailto:` 链接并去掉「待补充」字样。

## 约束备忘

- 页面不引用任何外部资源（无 CDN、无在线字体、无图片外链），字体为系统字体栈；改动时保持这一点。
- 文案语气与能力表述须与仓库 `README.md` 一致，不写未经核实的性能数字与客户案例。
- `assets/main.js` 只做：移动端导航开关、代码块复制按钮、滚动入场。不要引入第三方库。

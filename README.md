# Bingo

Bingo 是一个 Bing 每日壁纸服务，支持按日期获取壁纸、随机选择壁纸、指定分辨率和查询图片信息。可通过 Docker 或独立程序部署。

## 快速开始

安装 Docker 和 Docker Compose 后，在项目目录中运行：

```bash
docker compose up -d --build
```

服务默认监听 `8080` 端口。访问 <http://localhost:8080/> 即可跳转到当天的壁纸。

下载当天的壁纸：

```bash
curl -L 'http://localhost:8080/' -o wallpaper.jpg
```

## 接口使用

接口路径为 `/`，支持 `GET` 和 `HEAD`。以下示例使用 `http://localhost:8080`，远程部署时替换为实际服务地址。

### 请求参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `day` | `0` | 图片日期索引，取值为整数 `-1` 到 `7`；`0` 表示当天，`1` 表示前一天 |
| `rand` | `false` | 设置为 `true` 时随机选择 `-1` 到 `7` 的日期索引，并忽略 `day` |
| `size` | `1920x1080` | 图片分辨率，例如 `1366x768`、`1920x1080` 或 `UHD`；可用尺寸取决于 Bing |
| `info` | `false` | 设置为 `true` 时返回图片信息 JSON，并忽略 `direct` |
| `direct` | `true` | 默认跳转到图片地址；设置为 `false` 时直接返回 JPEG 图片内容 |

### 下载壁纸

下载前一天的壁纸，并指定分辨率：

```bash
curl -L 'http://localhost:8080/?day=1&size=1366x768' -o wallpaper.jpg
```

通过服务直接下载随机 UHD 壁纸：

```bash
curl 'http://localhost:8080/?rand=true&size=UHD&direct=false' -o wallpaper-uhd.jpg
```

### 查询图片信息

```bash
curl 'http://localhost:8080/?size=UHD&info=true'
```

响应示例：

```json
{
  "title": "图片描述及版权信息",
  "url": "https://www.bing.com/th?id=OHR.Example_UHD.jpg&w=3840&h=2160&c=8&rs=1&o=3&r=0",
  "link": "https://www.bing.com/search?q=example",
  "time": "20261003"
}
```

| 字段 | 说明 |
| --- | --- |
| `title` | 图片描述及版权信息 |
| `url` | 指定分辨率的图片地址 |
| `link` | 图片相关信息页面地址 |
| `time` | 图片日期，格式为 `YYYYMMDD` |

### 响应状态

| HTTP 状态码 | 说明 |
| --- | --- |
| `200` | 成功返回图片内容或图片信息 |
| `302` | 跳转到图片地址 |
| `400` | 日期、分辨率或查询参数格式不正确 |
| `404` | 请求的接口路径不存在 |
| `405` | 请求方法不受支持，请使用 `GET` 或 `HEAD` |
| `502` | 无法获取有效的 Bing 图片或图片信息 |
| `504` | 访问 Bing 超时 |

## 部署方式

### Docker Compose

在项目目录中启动服务：

```bash
docker compose up -d --build
```

使用其他宿主机端口：

```bash
PORT=8090 docker compose up -d --build
```

也可以在项目目录的 `.env` 文件中设置端口和服务配置：

```dotenv
PORT=8090
HTTP_TIMEOUT=15s
BING_BASE_URL=https://www.bing.com
```

### 自行构建 Docker 镜像

```bash
docker build -t bingo:local .
docker run -d --name bingo --restart unless-stopped \
  -p 8080:8080 \
  bingo:local
```

### 使用 GHCR 镜像

镜像发布后，可直接拉取并启动。将 `<owner>` 和 `<repository>` 替换为镜像所属账号和仓库名称，均使用小写：

```bash
docker run -d --name bingo --restart unless-stopped \
  -p 8080:8080 \
  'ghcr.io/<owner>/<repository>:latest'
```

如需固定版本，将 `latest` 替换为对应的版本标签，例如 `v1.0.0`。拉取私有镜像前，需先通过 `docker login ghcr.io` 登录有权访问该镜像的账号。

### 本地运行

需要 Go 1.25 或更高版本。在项目目录中运行：

```bash
go run .
```

或编译后运行：

```bash
go build -o bin/bingo .
./bin/bingo
```

## 配置

服务通过环境变量配置：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `LISTEN_ADDR` | `:8080` | HTTP 监听地址，例如 `127.0.0.1:8090` |
| `BING_BASE_URL` | `https://www.bing.com` | Bing 服务地址，须为可信的 HTTP(S) 地址，不包含路径、查询参数或账号密码 |
| `HTTP_TIMEOUT` | `10s` | 获取图片信息和图片内容的总超时时间，例如 `15s` |

本地运行时设置超时时间：

```bash
HTTP_TIMEOUT=15s go run .
```

使用 Docker 时通过 `-e` 设置环境变量：

```bash
docker run -d --name bingo --restart unless-stopped \
  -p 8080:8080 \
  -e HTTP_TIMEOUT=15s \
  bingo:local
```

Docker Compose 支持通过 `.env` 设置 `PORT`、`BING_BASE_URL` 和 `HTTP_TIMEOUT`，容器内的监听端口固定为 `8080`。

图片地址和上游请求限定为 `BING_BASE_URL` 配置的协议、域名及端口。上游重定向到其他地址时，接口返回 `502`。

## 镜像发布

仓库提供 [GitHub Actions 发布工作流](.github/workflows/docker-publish.yml)。启用 Actions 后，推送 Git tag 即可触发镜像构建和 GHCR 发布，支持 `linux/amd64` 和 `linux/arm64`。

```bash
git tag v1.0.0
git push origin v1.0.0
```

镜像地址为 `ghcr.io/<owner>/<repository>`，账号和仓库名称自动转换为小写。

| Git tag 示例 | 镜像标签 |
| --- | --- |
| `v1.0.0` | `v1.0.0`、`1.0.0`、`1.0`、`latest` |
| `v1.1.0-beta.1` | `v1.1.0-beta.1`、`1.1.0-beta.1` |
| 其他 tag | 对应的 Docker 标签，不支持的字符会被转换 |

`latest` 指向最近发布的稳定版本，稳定版本 tag 格式为 `v1.2.3` 或 `1.2.3`。预发布版本不会更新 `latest` 或主次版本标签。

工作流使用内置的 `GITHUB_TOKEN` 发布镜像。组织仓库需要允许 Actions 创建或写入包；如果同名包已经存在，需要授予当前仓库的 Actions 写入权限。

GHCR 包首次发布后默认为私有。如需允许公开拉取，在包设置中将可见性改为 Public。权限和可见性设置详见 [GHCR 文档](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

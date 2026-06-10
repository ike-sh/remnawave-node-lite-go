package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"remnawave-node-lite-go/internal/config"
	"remnawave-node-lite-go/internal/netadmin"
	"remnawave-node-lite-go/internal/version"
)

const defaultEnvPath = "/etc/remnanode/node.env"
const defaultUnitPath = "/etc/systemd/system/remnawave-node.service"

type result struct {
	level   string
	title   string
	detail  string
	fixHint string
}

// Run performs deployment health checks and returns exit code 0 (ok) or 1 (errors).
func Run(args []string) int {
	envPath := defaultEnvPath
	for i := 0; i < len(args); i++ {
		if args[i] == "--env" && i+1 < len(args) {
			envPath = args[i+1]
			i++
		}
	}
	if override := strings.TrimSpace(os.Getenv("REMNANODE_ENV")); override != "" {
		envPath = override
	}

	fmt.Println(version.String())
	fmt.Println("── 部署自检 ──")

	var results []result

	results = append(results, checkSystemdCapNetAdmin())
	results = append(results, checkCapNetAdmin())

	cfg, cfgErr := loadConfig(envPath)
	if cfgErr != nil {
		results = append(results, result{
			level:   "ERROR",
			title:   "配置文件",
			detail:  cfgErr.Error(),
			fixHint: "创建 " + envPath + " 或指定 --env PATH",
		})
	} else {
		results = append(results, checkSecret(cfg)...)
		results = append(results, checkXrayBinary(cfg.XrayBin)...)
		results = append(results, checkGeoFiles(cfg.GeoDir)...)
		results = append(results, checkPersistedStart(cfg.DataDir)...)
		results = append(results, checkCommand("nft", "nftables 命令行（插件 IP 封禁）")...)
		results = append(results, checkCommand("ss", "ss 命令（踢连接 drop-ips）")...)
	}

	exitCode := 0
	for _, item := range results {
		fmt.Printf("[%s] %s", item.level, item.title)
		if item.detail != "" {
			fmt.Printf(" — %s", item.detail)
		}
		fmt.Println()
		if item.fixHint != "" {
			fmt.Printf("      → %s\n", item.fixHint)
		}
		if item.level == "ERROR" {
			exitCode = 1
		}
	}

	if exitCode == 0 {
		fmt.Println("── 结论：核心项通过（WARN 项不影响 Panel 基本连接）──")
	} else {
		fmt.Println("── 结论：存在 ERROR，请先修复后再接入 Panel ──")
	}
	return exitCode
}

func loadConfig(envPath string) (config.Config, error) {
	if _, err := os.Stat(envPath); err != nil {
		if envPath != ".env" {
			if _, err2 := os.Stat(".env"); err2 == nil {
				return config.Load(".env")
			}
		}
		return config.Config{}, fmt.Errorf("找不到 %s", envPath)
	}
	return config.Load(envPath)
}

func checkCapNetAdmin() result {
	if netadmin.HasCapNetAdmin() {
		return result{level: "OK", title: "CAP_NET_ADMIN", detail: "当前进程已具备"}
	}
	return result{
		level:   "WARN",
		title:   "CAP_NET_ADMIN",
		detail:  "当前进程未具备（nftables / ss -K 不可用）",
		fixHint: "通过 systemd 启动：确认 unit 含 AmbientCapabilities=CAP_NET_ADMIN，然后 systemctl daemon-reload && systemctl restart remnawave-node",
	}
}

func checkSystemdCapNetAdmin() result {
	data, err := os.ReadFile(defaultUnitPath)
	if err != nil {
		return result{
			level:   "WARN",
			title:   "systemd unit",
			detail:  defaultUnitPath + " 未找到",
			fixHint: "运行 install-node.sh 或 upgrade.sh 安装官方 unit",
		}
	}
	content := string(data)
	if strings.Contains(content, "AmbientCapabilities=CAP_NET_ADMIN") {
		return result{level: "OK", title: "systemd unit", detail: "已配置 AmbientCapabilities=CAP_NET_ADMIN"}
	}
	return result{
		level:   "WARN",
		title:   "systemd unit",
		detail:  "未包含 AmbientCapabilities=CAP_NET_ADMIN",
		fixHint: "sudo curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v" + version.Version + "/deploy/remnawave-node.service -o " + defaultUnitPath + " && sudo systemctl daemon-reload && sudo systemctl restart remnawave-node",
	}
}

func checkSecret(cfg config.Config) []result {
	if strings.TrimSpace(cfg.SecretKey) != "" {
		return []result{{level: "OK", title: "Secret Key", detail: "已配置"}}
	}
	return []result{{
		level:   "ERROR",
		title:   "Secret Key",
		detail:  "未配置（SECRET_KEY 或 SECRET_KEY_FILE 为空）",
		fixHint: "编辑 /etc/remnanode/secret.key 粘贴 Panel 下发的 Key，然后 systemctl restart remnawave-node",
	}}
}

func checkPersistedStart(dataDir string) []result {
	if dataDir == "" {
		dataDir = "/var/lib/remnanode"
	}
	path := filepath.Join(dataDir, "last-start.json")
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []result{{
				level:   "WARN",
				title:   "重启自动恢复",
				detail:  path + " 不存在",
				fixHint: "Panel 启用节点一次（若装完仍离线则禁用→启用）；成功 xray/start 后生成 last-start.json，reboot 即可自动恢复 rw-core",
			}}
		}
		return []result{{
			level:   "WARN",
			title:   "重启自动恢复",
			detail:  "无法读取 " + path + ": " + err.Error(),
		}}
	}
	return []result{{
		level:  "OK",
		title:  "重启自动恢复",
		detail: fmt.Sprintf("%s 存在 (%d bytes)", path, info.Size()),
	}}
}

func checkXrayBinary(bin string) []result {
	if bin == "" {
		bin = "/usr/local/bin/rw-core"
	}
	info, err := os.Stat(bin)
	if err != nil {
		return []result{{
			level:   "ERROR",
			title:   "rw-core",
			detail:  bin + " 不存在",
			fixHint: "运行 scripts/install-xray.sh 或 install-node.sh（勿加 --skip-xray）",
		}}
	}
	if info.Mode()&0o111 == 0 {
		return []result{{
			level:   "ERROR",
			title:   "rw-core",
			detail:  bin + " 不可执行",
			fixHint: "sudo chmod +x " + bin,
		}}
	}
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return []result{{
			level:   "WARN",
			title:   "rw-core",
			detail:  bin + " 存在但 version 命令失败",
		}}
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	return []result{{level: "OK", title: "rw-core", detail: line}}
}

func checkGeoFiles(dir string) []result {
	if dir == "" {
		dir = "/usr/local/share/xray"
	}
	var missing []string
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return []result{{level: "OK", title: "Geo 数据", detail: dir + " 含 geoip.dat / geosite.dat"}}
	}
	return []result{{
		level:   "WARN",
		title:   "Geo 数据",
		detail:  "缺少 " + strings.Join(missing, ", "),
		fixHint: "重新运行 install-xray.sh 或从 Xray 发行版复制到 " + dir,
	}}
}

func checkCommand(name, purpose string) []result {
	if path, err := exec.LookPath(name); err == nil {
		return []result{{level: "OK", title: name, detail: path + "（" + purpose + "）"}}
	}
	return []result{{
		level:   "WARN",
		title:   name,
		detail:  "未安装（" + purpose + "）",
		fixHint: "Debian/Ubuntu: apt install iproute2 " + name,
	}}
}

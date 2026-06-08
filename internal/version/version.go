package version

import "fmt"

const Version = "0.8.2"

const releaseRepo = "ike-sh/remnawave-node-lite-go"

func String() string {
	return "remnawave-node-lite-go " + Version
}

func ReleaseAssetURL(tag, arch string) string {
	return fmt.Sprintf(
		"https://github.com/%s/releases/download/%s/remnanode-lite_linux_%s.tar.gz",
		releaseRepo,
		tag,
		arch,
	)
}

func InstallScriptURL(tag, script string) string {
	return fmt.Sprintf(
		"https://raw.githubusercontent.com/%s/%s/scripts/%s",
		releaseRepo,
		tag,
		script,
	)
}

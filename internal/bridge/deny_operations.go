package bridge

import (
	"strings"
)

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func inspectOperation(program string, args []denyArg, cwd string) *CommandRestriction {
	if strings.HasPrefix(program, "kafka-") {
		program = strings.TrimSuffix(program, ".sh")
	}
	switch {
	case program == "mkfs" || strings.HasPrefix(program, "mkfs.") || contains([]string{"mke2fs", "mkswap", "wipefs", "shred", "blkdiscard", "fdisk", "sfdisk", "parted", "sgdisk"}, program):
		return deny("disk-destructive", "禁止格式化、擦除或分区工具")
	case contains([]string{"shutdown", "reboot", "poweroff", "halt", "killall", "pkill"}, program):
		return deny("host-disruption", "禁止关机重启或按名称批量终止进程")
	}
	switch program {
	case "rm", "dd", "chmod", "chown", "chgrp", "systemctl", "init", "telinit", "kill", "find", "docker", "podman", "kubectl", "terraform", "etcdctl", "kafka-topics", "kafka-delete-records", "kafka-consumer-groups", "kafka-acls", "aws":
	default:
		return nil
	}
	a, ok := literalArgs(args)
	if !ok {
		a = make([]string, len(args))
		for i, arg := range args {
			if arg.literal {
				a[i] = arg.value
			}
		}
	}
	switch program {
	case "rm":
		recursive, force, options := false, false, true
		var targets []string
		for _, v := range a {
			if options && v == "--" {
				options = false
				continue
			}
			if options && strings.HasPrefix(v, "--") {
				switch v {
				case "--recursive":
					recursive = true
				case "--force":
					force = true
				case "--interactive", "--interactive=always", "--interactive=once", "--interactive=never", "--verbose", "--dir", "--one-file-system", "--preserve-root", "--no-preserve-root":
				default:
					ok = false // unknown options require human review
				}
				continue
			}
			if options && strings.HasPrefix(v, "-") && v != "-" {
				recursive = recursive || strings.ContainsAny(v[1:], "rR")
				force = force || strings.Contains(v[1:], "f")
			} else {
				targets = append(targets, v)
			}
		}
		if recursive && force {
			return deny("recursive-force-remove", "禁止递归强制删除（rm -rf 及等价参数）")
		}
		if recursive {
			for _, target := range targets {
				if !strings.HasPrefix(target, "/") {
					ok = false
					continue
				}
				if criticalPath(target, cwd) {
					return deny("critical-tree", "禁止递归删除关键目录")
				}
			}
		}
	case "chmod", "chown", "chgrp":
		for _, v := range a {
			if v == "--recursive" || strings.HasPrefix(v, "-") && !strings.HasPrefix(v, "--") && strings.Contains(v, "R") {
				for _, target := range a {
					if strings.HasPrefix(target, "/") && criticalPath(target, cwd) {
						return deny("critical-permissions", "禁止递归修改关键目录的权限或归属")
					}
				}
			}
		}
	case "dd":
		for _, v := range a {
			if strings.HasPrefix(v, "of=") && devicePath(strings.TrimPrefix(v, "of="), cwd) {
				return deny("device-write", "禁止直接写入设备路径")
			}
		}
	case "systemctl":
		for _, v := range a {
			if contains([]string{"reboot", "poweroff", "halt", "kexec", "exit", "rescue", "emergency", "isolate", "suspend", "hibernate", "hybrid-sleep"}, v) {
				return deny("host-disruption", "禁止改变整机运行状态")
			}
		}
	case "init", "telinit":
		if contains(a, "0") || contains(a, "6") {
			return deny("host-disruption", "禁止切换到关机或重启运行级别")
		}
	case "kill":
		for _, v := range a {
			if v == "-1" || v == "0" {
				return deny("mass-kill", "禁止向所有进程或当前进程组发送信号")
			}
		}
	case "find":
		for i, v := range a {
			if v == "-delete" {
				return deny("find-delete", "禁止 find 批量删除")
			}
			if contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, v) {
				end := i + 1
				for end < len(a) && a[end] != ";" && a[end] != "+" {
					end++
				}
				if d := inspectArgs(args[i+1:end], nil, cwd, 0); d != nil {
					return d
				}
			}
		}
	case "docker", "podman":
		command, ok := operationArgs(a, " -H --host --context --config --log-level -c ", " -D --debug --tls --tlsverify ")
		if !ok {
			return uninspectable()
		}
		if len(command) > 0 {
			verb := command[0]
			if contains([]string{"rm", "rmi", "kill", "prune"}, verb) {
				return deny("container-destructive", "禁止删除或中断容器及其资源")
			}
			if len(command) > 1 && contains([]string{"system", "container", "image", "volume", "network", "builder", "buildx"}, verb) && contains([]string{"rm", "prune", "kill"}, command[1]) {
				return deny("container-destructive", "禁止删除或清理容器资源")
			}
			if verb == "compose" && (contains(command, "down") || contains(command, "rm") || contains(command, "kill")) {
				return deny("container-destructive", "禁止批量移除或强制停止 Compose 服务")
			}
			if contains([]string{"exec", "run"}, verb) {
				return manualInspection("nested-execution", "容器内部执行环境及入口程序需人工核对")
			}
		}
	case "kubectl":
		command, ok := operationArgs(a, " -n --namespace --context --kubeconfig --cluster --user --server --token --request-timeout ", " --insecure-skip-tls-verify ")
		if !ok {
			return uninspectable()
		}
		if len(command) > 0 && (contains([]string{"delete", "drain", "debug"}, command[0]) || command[0] == "replace" && contains(command, "--force")) {
			return deny("cluster-destructive", "禁止删除、批量变更集群资源或嵌套执行")
		}
	case "terraform":
		command, ok := operationArgs(a, " -chdir ", " ")
		if !ok {
			return uninspectable()
		}
		if len(command) > 0 && contains([]string{"destroy", "taint", "force-unlock"}, command[0]) {
			return deny("infrastructure-destructive", "禁止销毁、变更基础设施或修改状态")
		}
	case "etcdctl":
		command, parsed := operationArgs(a, " --endpoints --user --password --cacert --cert --key --dial-timeout --command-timeout --write-out -w ", " --insecure-transport --insecure-skip-tls-verify ")
		if !parsed || len(command) == 0 {
			return uninspectable()
		}
		verb := command[0]
		if contains([]string{"del", "delete"}, verb) && (contains(command, "--prefix") || contains(command, "--from-key")) || contains([]string{"compact", "defrag", "move-leader"}, verb) {
			return deny("etcd-destructive", "禁止 etcd 范围删除或集群维护变更")
		}
		if len(command) > 1 && contains([]string{"auth", "user", "role", "member", "snapshot", "alarm"}, verb) && !contains([]string{"get", "list", "status"}, command[1]) {
			return deny("etcd-administration", "禁止 etcd 权限、成员、快照或告警状态变更")
		}
	case "kafka-topics", "kafka-delete-records", "kafka-consumer-groups", "kafka-acls":
		if program == "kafka-delete-records" || contains(a, "--delete") || contains(a, "--delete-offsets") || contains(a, "--reset-offsets") && contains(a, "--execute") || program == "kafka-acls" && (contains(a, "--add") || contains(a, "--remove")) {
			return deny("kafka-destructive", "禁止 Kafka 主题、消息、消费组删除、位点执行重置或权限变更")
		}
	case "aws":
		command, parsed := operationArgs(a, " --profile --region --endpoint-url --output --query --ca-bundle --cli-connect-timeout --cli-read-timeout ", " --no-cli-pager --no-verify-ssl --debug ")
		if !parsed {
			return uninspectable()
		}
		if len(command) > 1 && (command[0] == "s3" && (command[1] == "rm" && contains(command, "--recursive") || command[1] == "rb") || command[0] == "s3api" && contains([]string{"delete-bucket", "delete-objects", "put-bucket-policy", "delete-bucket-policy"}, command[1])) {
			return deny("object-storage-destructive", "禁止对象存储批量删除、删桶或桶权限变更")
		}
	}
	if !ok {
		return uninspectable()
	}
	return nil
}

func operationArgs(args []string, withValue, flags string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		v := args[i]
		if v == "--" {
			return args[i+1:], true
		}
		if !strings.HasPrefix(v, "-") {
			return args[i:], true
		}
		if strings.Contains(flags, " "+v+" ") {
			continue
		}
		if key, _, ok := strings.Cut(v, "="); ok && strings.Contains(withValue, " "+key+" ") {
			continue
		}
		if strings.Contains(withValue, " "+v+" ") && i+1 < len(args) {
			i++
			continue
		}
		return nil, false
	}
	return nil, true
}

func inspectRedis(args []denyArg) *CommandRestriction {
	a, ok := literalArgs(args)
	if !ok {
		return uninspectable()
	}
	command, ok := operationArgs(a, " -h -p -s -a -u -n --user --pass --askpass --sni --cacert --cert --key ", " --raw --no-raw --csv --json --tls --insecure ")
	if !ok || len(command) == 0 {
		return uninspectable()
	}
	verb := strings.ToUpper(command[0])
	if contains([]string{"FLUSHALL", "FLUSHDB", "SHUTDOWN", "DEBUG", "MODULE", "MIGRATE", "REPLICAOF", "SLAVEOF", "FAILOVER", "SWAPDB"}, verb) {
		return deny("redis-destructive", "禁止 Redis 清库、脚本执行或实例管理操作")
	}
	if contains([]string{"EVAL", "EVAL_RO"}, verb) && len(command) > 1 {
		if d := inspectScript(command[1], "", 0, "redis"); d != nil && d.Decision == "deny" {
			return d
		}
		return manualInspection("redis-script", "Redis Lua 脚本须人工检查")
	}
	if contains([]string{"EVALSHA", "EVALSHA_RO", "FCALL", "FCALL_RO"}, verb) {
		return manualInspection("redis-script", "无法读取 Redis 服务端脚本或函数内容")
	}
	if contains([]string{"SCRIPT", "FUNCTION"}, verb) {
		if len(command) > 1 && contains([]string{"FLUSH", "DELETE", "KILL"}, strings.ToUpper(command[1])) {
			return deny("redis-destructive", "禁止清除 Redis 脚本、函数或中断执行")
		}
		return manualInspection("redis-script", "Redis 脚本或函数管理须人工检查")
	}
	if contains([]string{"CONFIG", "ACL", "CLUSTER"}, verb) {
		if len(command) < 2 || !contains([]string{"GET", "LIST", "INFO", "NODES", "SLOTS", "SHARDS", "WHOAMI", "USERS", "GETUSER", "HELP"}, strings.ToUpper(command[1])) {
			return deny("redis-administration", "禁止 Redis 配置、权限或集群变更")
		}
	}
	return nil
}

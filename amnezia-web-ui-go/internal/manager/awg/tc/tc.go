package tc

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/devops-igor/amnezia-web-ui-go/internal/manager/ssh"
)

const (
	DefaultInterface  = "awg0"
	DefaultContainer  = "amnezia-awg"
	IFBDevice         = "ifb0"
	DefaultClassID    = 9999
	DefaultClassRate  = "10gbit"
	GlobalPoolClassID = 1
	ClassIDOffset     = 100
)

// PeerToClassID converts a peer IP address (e.g. "10.8.1.45") to an HTB class ID.
func PeerToClassID(peerIP string) (int, error) {
	parts := strings.Split(peerIP, ".")
	if len(parts) != 4 {
		return 0, fmt.Errorf("invalid IP address: %s", peerIP)
	}
	lastOctet, err := strconv.Atoi(parts[3])
	if err != nil || lastOctet < 1 || lastOctet > 253 {
		return 0, fmt.Errorf("IP last octet %s out of usable range 1-253 in: %s", parts[3], peerIP)
	}
	return lastOctet + ClassIDOffset, nil
}

func tcExec(ctx context.Context, sshClient ssh.SSHClient, containerName, args string) (string, string, int, error) {
	if containerName == "" {
		containerName = DefaultContainer
	}
	cmd := fmt.Sprintf("docker exec -i %s tc %s", containerName, args)
	return sshClient.RunSudoCommand(ctx, cmd)
}

func ipExec(ctx context.Context, sshClient ssh.SSHClient, containerName, args string) (string, string, int, error) {
	if containerName == "" {
		containerName = DefaultContainer
	}
	cmd := fmt.Sprintf("docker exec -i %s ip %s", containerName, args)
	return sshClient.RunSudoCommand(ctx, cmd)
}

// SetupIFB creates ifb0 and redirects awg0 ingress to ifb0 for upload traffic shaping.
func SetupIFB(ctx context.Context, sshClient ssh.SSHClient, containerName string) error {
	if containerName == "" {
		containerName = DefaultContainer
	}

	// 1. Check if ifb0 exists
	out, _, code, _ := ipExec(ctx, sshClient, containerName, "link show dev ifb0")
	if code != 0 || !strings.Contains(out, "ifb0") {
		_, errOut, cCode, err := ipExec(ctx, sshClient, containerName, "link add ifb0 type ifb")
		if err != nil || (cCode != 0 && !strings.Contains(errOut, "File exists")) {
			return fmt.Errorf("failed to create ifb0 (code %d): %s, %w", cCode, errOut, err)
		}
	}

	// 2. Bring ifb0 up
	if _, errOut, code, err := ipExec(ctx, sshClient, containerName, "link set ifb0 up"); err != nil || code != 0 {
		return fmt.Errorf("failed to set ifb0 up (code %d): %s, %w", code, errOut, err)
	}

	// 3. Setup ingress qdisc on awg0
	qdiscOut, _, qCode, _ := tcExec(ctx, sshClient, containerName, fmt.Sprintf("qdisc show dev %s", DefaultInterface))
	if qCode != 0 || !strings.Contains(qdiscOut, "ingress") {
		_, errOut, code, err := tcExec(ctx, sshClient, containerName, fmt.Sprintf("qdisc add dev %s handle ffff: ingress", DefaultInterface))
		if err != nil || (code != 0 && !strings.Contains(errOut, "File exists")) {
			return fmt.Errorf("failed to add ingress qdisc on awg0 (code %d): %s, %w", code, errOut, err)
		}
	}

	// 4. Add filter redirecting awg0 ingress to ifb0
	filterCmd := fmt.Sprintf("filter add dev %s parent ffff: protocol ip u32 match u32 0 0 action mirred egress redirect dev %s", DefaultInterface, IFBDevice)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, filterCmd); err != nil || (code != 0 && !strings.Contains(errOut, "File exists")) {
		return fmt.Errorf("failed to add ingress redirect filter (code %d): %s, %w", code, errOut, err)
	}

	return nil
}

// TeardownIFB removes the ingress redirect and deletes ifb0.
func TeardownIFB(ctx context.Context, sshClient ssh.SSHClient, containerName string) error {
	if containerName == "" {
		containerName = DefaultContainer
	}

	_, _, _, _ = tcExec(ctx, sshClient, containerName, fmt.Sprintf("qdisc del dev %s handle ffff: ingress", DefaultInterface))
	_, _, _, _ = ipExec(ctx, sshClient, containerName, "link del ifb0")
	return nil
}

// SetupQdisc creates root HTB qdisc with global pool and default unlimited class on an interface.
func SetupQdisc(ctx context.Context, sshClient ssh.SSHClient, containerName, iface string, globalLimitMbps *int) error {
	if containerName == "" {
		containerName = DefaultContainer
	}
	if iface == "" {
		iface = DefaultInterface
	}

	// Check if HTB already exists
	out, _, code, _ := tcExec(ctx, sshClient, containerName, fmt.Sprintf("qdisc show dev %s", iface))
	if code == 0 && strings.Contains(out, "htb") {
		return nil
	}

	// Remove old qdisc
	_, _, _, _ = tcExec(ctx, sshClient, containerName, fmt.Sprintf("qdisc del dev %s root 2>/dev/null", iface))

	poolRate := DefaultClassRate
	if globalLimitMbps != nil && *globalLimitMbps > 0 {
		poolRate = fmt.Sprintf("%dmbit", *globalLimitMbps)
	}

	// Add root HTB qdisc
	qdiscCmd := fmt.Sprintf("qdisc add dev %s root handle 1: htb default %d", iface, DefaultClassID)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, qdiscCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add HTB qdisc on %s (code %d): %s, %w", iface, code, errOut, err)
	}

	// Add global pool class 1:1
	poolCmd := fmt.Sprintf("class add dev %s parent 1: classid 1:%d htb rate %s", iface, GlobalPoolClassID, poolRate)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, poolCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add global pool class on %s (code %d): %s, %w", iface, code, errOut, err)
	}

	// Add default unlimited class 1:9999
	defaultCmd := fmt.Sprintf("class add dev %s parent 1:%d classid 1:%d htb rate %s ceil %s", iface, GlobalPoolClassID, DefaultClassID, DefaultClassRate, poolRate)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, defaultCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add default class on %s (code %d): %s, %w", iface, code, errOut, err)
	}

	return nil
}

func findFilterHandles(ctx context.Context, sshClient ssh.SSHClient, containerName, iface string, classID int) []string {
	out, _, _, err := tcExec(ctx, sshClient, containerName, fmt.Sprintf("filter show dev %s parent 1:", iface))
	if err != nil || out == "" {
		return nil
	}

	var handles []string
	targetFlow := fmt.Sprintf("flowid 1:%d", classID)
	fhRegex := regexp.MustCompile(`fh ([0-9a-f]+::[0-9a-f]+)`)

	entries := strings.Split(out, "filter ")
	for _, entry := range entries {
		if strings.Contains(entry, targetFlow) {
			if m := fhRegex.FindStringSubmatch(entry); len(m) > 1 {
				handles = append(handles, m[1])
			}
		}
	}
	return handles
}

// RemoveSpeedLimit removes HTB class and filters for a peer IP from both awg0 and ifb0.
func RemoveSpeedLimit(ctx context.Context, sshClient ssh.SSHClient, containerName, iface, peerIP string) error {
	if containerName == "" {
		containerName = DefaultContainer
	}

	classID, err := PeerToClassID(peerIP)
	if err != nil {
		return err
	}

	// Remove on awg0 (download)
	handlesDown := findFilterHandles(ctx, sshClient, containerName, DefaultInterface, classID)
	for _, h := range handlesDown {
		delFilterCmd := fmt.Sprintf("filter del dev %s parent 1: protocol ip prio 1 handle %s u32", DefaultInterface, h)
		_, _, _, _ = tcExec(ctx, sshClient, containerName, delFilterCmd)
	}
	delClassDown := fmt.Sprintf("class del dev %s parent 1:%d classid 1:%d", DefaultInterface, GlobalPoolClassID, classID)
	_, _, _, _ = tcExec(ctx, sshClient, containerName, delClassDown)

	// Remove on ifb0 (upload)
	handlesUp := findFilterHandles(ctx, sshClient, containerName, IFBDevice, classID)
	for _, h := range handlesUp {
		delFilterCmd := fmt.Sprintf("filter del dev %s parent 1: protocol ip prio 1 handle %s u32", IFBDevice, h)
		_, _, _, _ = tcExec(ctx, sshClient, containerName, delFilterCmd)
	}
	delClassUp := fmt.Sprintf("class del dev %s parent 1:%d classid 1:%d", IFBDevice, GlobalPoolClassID, classID)
	_, _, _, _ = tcExec(ctx, sshClient, containerName, delClassUp)

	return nil
}

// ApplySpeedLimit configures HTB speed limits for a peer IP on both download (awg0) and upload (ifb0).
func ApplySpeedLimit(ctx context.Context, sshClient ssh.SSHClient, containerName, iface, peerIP string, downMbps, upMbps int) error {
	if containerName == "" {
		containerName = DefaultContainer
	}

	classID, err := PeerToClassID(peerIP)
	if err != nil {
		return err
	}

	if err := SetupIFB(ctx, sshClient, containerName); err != nil {
		return err
	}
	if err := SetupQdisc(ctx, sshClient, containerName, DefaultInterface, nil); err != nil {
		return err
	}
	if err := SetupQdisc(ctx, sshClient, containerName, IFBDevice, nil); err != nil {
		return err
	}

	_ = RemoveSpeedLimit(ctx, sshClient, containerName, iface, peerIP)

	// Download on awg0
	classDownCmd := fmt.Sprintf("class add dev %s parent 1:%d classid 1:%d htb rate %dmbit ceil %dmbit", DefaultInterface, GlobalPoolClassID, classID, downMbps, downMbps)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, classDownCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add download class for %s (code %d): %s, %w", peerIP, code, errOut, err)
	}
	filterDownCmd := fmt.Sprintf("filter add dev %s parent 1: protocol ip prio 1 u32 match ip dst %s/32 flowid 1:%d", DefaultInterface, peerIP, classID)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, filterDownCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add download filter for %s (code %d): %s, %w", peerIP, code, errOut, err)
	}

	// Upload on ifb0
	classUpCmd := fmt.Sprintf("class add dev %s parent 1:%d classid 1:%d htb rate %dmbit ceil %dmbit", IFBDevice, GlobalPoolClassID, classID, upMbps, upMbps)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, classUpCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add upload class for %s (code %d): %s, %w", peerIP, code, errOut, err)
	}
	filterUpCmd := fmt.Sprintf("filter add dev %s parent 1: protocol ip prio 1 u32 match ip src %s/32 flowid 1:%d", IFBDevice, peerIP, classID)
	if _, errOut, code, err := tcExec(ctx, sshClient, containerName, filterUpCmd); err != nil || code != 0 {
		return fmt.Errorf("failed to add upload filter for %s (code %d): %s, %w", peerIP, code, errOut, err)
	}

	return nil
}

// SetGlobalLimit updates the global pool class 1:1 on both awg0 and ifb0.
func SetGlobalLimit(ctx context.Context, sshClient ssh.SSHClient, containerName string, downMbps, upMbps *int) error {
	if containerName == "" {
		containerName = DefaultContainer
	}

	downRate := DefaultClassRate
	if downMbps != nil && *downMbps > 0 {
		downRate = fmt.Sprintf("%dmbit", *downMbps)
	}

	upRate := DefaultClassRate
	if upMbps != nil && *upMbps > 0 {
		upRate = fmt.Sprintf("%dmbit", *upMbps)
	}

	// Update awg0 pool and default ceil
	_, _, _, _ = tcExec(ctx, sshClient, containerName, fmt.Sprintf("class change dev %s parent 1: classid 1:%d htb rate %s", DefaultInterface, GlobalPoolClassID, downRate))
	_, _, _, _ = tcExec(ctx, sshClient, containerName, fmt.Sprintf("class change dev %s parent 1:%d classid 1:%d htb rate %s ceil %s", DefaultInterface, GlobalPoolClassID, DefaultClassID, DefaultClassRate, downRate))

	// Update ifb0 pool and default ceil
	_, _, _, _ = tcExec(ctx, sshClient, containerName, fmt.Sprintf("class change dev %s parent 1: classid 1:%d htb rate %s", IFBDevice, GlobalPoolClassID, upRate))
	_, _, _, _ = tcExec(ctx, sshClient, containerName, fmt.Sprintf("class change dev %s parent 1:%d classid 1:%d htb rate %s ceil %s", IFBDevice, GlobalPoolClassID, DefaultClassID, DefaultClassRate, upRate))

	return nil
}

// BuildBatchTCScript constructs the 2-phase shell scripts for applying all TC limits in batch.
func BuildBatchTCScript(containerName string, clients []map[string]any, globalLimitDown, globalLimitUp *int) (infraScript, clientScript string) {
	if containerName == "" {
		containerName = DefaultContainer
	}

	downRate := DefaultClassRate
	if globalLimitDown != nil && *globalLimitDown > 0 {
		downRate = fmt.Sprintf("%dmbit", *globalLimitDown)
	}

	upRate := DefaultClassRate
	if globalLimitUp != nil && *globalLimitUp > 0 {
		upRate = fmt.Sprintf("%dmbit", *globalLimitUp)
	}

	infraCommands := []string{
		fmt.Sprintf("docker exec -i %s sh -c 'tc qdisc del dev %s root 2>/dev/null; tc qdisc del dev %s root 2>/dev/null; true'", containerName, DefaultInterface, IFBDevice),
		fmt.Sprintf("docker exec -i %s sh -c 'ip link add ifb0 type ifb 2>/dev/null; ip link set ifb0 up 2>/dev/null; true'", containerName),
		fmt.Sprintf("docker exec -i %s sh -c 'tc qdisc add dev %s handle ffff: ingress 2>/dev/null; tc filter add dev %s parent ffff: protocol ip u32 match u32 0 0 action mirred egress redirect dev %s 2>/dev/null; true'", containerName, DefaultInterface, DefaultInterface, IFBDevice),
		fmt.Sprintf("docker exec -i %s sh -c 'tc qdisc del dev %s root 2>/dev/null; tc qdisc add dev %s root handle 1: htb default %d; tc class add dev %s parent 1: classid 1:%d htb rate %s; tc class add dev %s parent 1:%d classid 1:%d htb rate %s ceil %s; true'", containerName, DefaultInterface, DefaultInterface, DefaultClassID, DefaultInterface, GlobalPoolClassID, downRate, DefaultInterface, GlobalPoolClassID, DefaultClassID, DefaultClassRate, downRate),
		fmt.Sprintf("docker exec -i %s sh -c 'tc qdisc del dev %s root 2>/dev/null; tc qdisc add dev %s root handle 1: htb default %d; tc class add dev %s parent 1: classid 1:%d htb rate %s; tc class add dev %s parent 1:%d classid 1:%d htb rate %s ceil %s; true'", containerName, IFBDevice, IFBDevice, DefaultClassID, IFBDevice, GlobalPoolClassID, upRate, IFBDevice, GlobalPoolClassID, DefaultClassID, DefaultClassRate, upRate),
	}

	var clientCommands []string
	for _, client := range clients {
		userData, _ := client["userData"].(map[string]any)
		peerIP, _ := client["clientIp"].(string)
		if peerIP == "" && userData != nil {
			peerIP, _ = userData["clientIp"].(string)
		}
		if peerIP == "" {
			continue
		}

		var speedDown, speedUp int
		if userData != nil {
			if v, ok := userData["speed_limit_down"].(int); ok {
				speedDown = v
			} else if v, ok := userData["speed_limit_down"].(float64); ok {
				speedDown = int(v)
			}
			if v, ok := userData["speed_limit_up"].(int); ok {
				speedUp = v
			} else if v, ok := userData["speed_limit_up"].(float64); ok {
				speedUp = int(v)
			}
		}

		if speedDown <= 0 && speedUp <= 0 {
			continue
		}
		if speedDown <= 0 {
			speedDown = speedUp
		}
		if speedUp <= 0 {
			speedUp = speedDown
		}

		classID, err := PeerToClassID(peerIP)
		if err != nil {
			continue
		}

		clientCommands = append(clientCommands, fmt.Sprintf("docker exec -i %s sh -c 'tc class add dev %s parent 1:%d classid 1:%d htb rate %dmbit ceil %dmbit; tc filter add dev %s parent 1: protocol ip prio 1 u32 match ip dst %s/32 flowid 1:%d; tc class add dev %s parent 1:%d classid 1:%d htb rate %dmbit ceil %dmbit; tc filter add dev %s parent 1: protocol ip prio 1 u32 match ip src %s/32 flowid 1:%d; true'",
			containerName,
			DefaultInterface, GlobalPoolClassID, classID, speedDown, speedDown,
			DefaultInterface, peerIP, classID,
			IFBDevice, GlobalPoolClassID, classID, speedUp, speedUp,
			IFBDevice, peerIP, classID,
		))
	}

	return strings.Join(infraCommands, "\n"), strings.Join(clientCommands, "\n")
}

// ReapplyAllLimits restores all TC rules and limits across the container.
func ReapplyAllLimits(ctx context.Context, sshClient ssh.SSHClient, containerName, iface string, clients []map[string]any, globalLimitDown, globalLimitUp *int) error {
	infraScript, clientScript := BuildBatchTCScript(containerName, clients, globalLimitDown, globalLimitUp)

	if infraScript != "" {
		if _, errOut, code, err := sshClient.RunSudoScript(ctx, infraScript); err != nil || code != 0 {
			return fmt.Errorf("failed to run infra TC script (code %d): %s, %w", code, errOut, err)
		}
	}

	if clientScript != "" {
		if _, errOut, code, err := sshClient.RunSudoScript(ctx, clientScript); err != nil || code != 0 {
			return fmt.Errorf("failed to run client TC script (code %d): %s, %w", code, errOut, err)
		}
	}

	return nil
}

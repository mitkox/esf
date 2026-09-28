package threatmon

import "regexp"

// This file holds the shipped ruleset. Every pattern is compiled once at
// package init into a package-level variable, and defaultRules assembles them
// into Rules. IDs are stable: they appear in findings, alerts, and quarantined
// run evidence, so renumbering them is a breaking change.
//
// False-positive discipline: the monitor runs on captured agent output, which
// includes source code, diffs, test fixtures, and documentation. A pattern that
// only mentions a dangerous word is a bad pattern, because reading a man page
// or editing this file's own ruleset is not an attack. Prefer a signal that
// requires an operation (a verb, a redirection, a path) over a bare token. Where
// a choice was unavoidable this file documents the residual risk next to the
// pattern rather than pretending it is exact.

var (
	// PE-001: writing /etc/sudoers grants durable, often passwordless root.
	// Signal: a shell redirection into the file, or a writer command whose
	// arguments reach it.
	// FP note: a documentation line that quotes a full "tee ... /etc/sudoers"
	// command verbatim will match. A bare mention of "sudoers" does not.
	reSudoersWrite = regexp.MustCompile(`(?i)(?:>>?\s*\S*/etc/sudoers\b|\b(?:tee|visudo|sed\s+-i|cp|mv|install)\b[^\n]{0,80}/etc/sudoers\b)`)

	// PE-002: granting elevated privilege to a file or binary. Signal: setuid
	// bits (symbolic or octal 4xxx), a setcap capability grant, or handing a
	// path to root.
	// FP note: chmod 4755 and chown root appear in legitimate install scripts;
	// severity is HIGH rather than CRITICAL for that reason.
	rePrivilegeBits = regexp.MustCompile(`(?i)(?:\bchmod\b[^\n]{0,40}(?:\bu\+s\b|\bg\+s\b|\bo\+s\b|\b4[0-7]{3}\b)|\bsetcap\b[^\n]{0,40}\bcap_[a-z]|\bchown\b[^\n]{0,40}\broot(?::root)?\b)`)

	// PE-003: loading kernel code or mounting a filesystem into the sandbox.
	// Signal: insmod/modprobe/rmmod with a module argument, or a mount
	// invocation with a device, bind, or kernel filesystem.
	// FP note: the module name argument is required, so dmesg-style output such
	// as "modprobe: FATAL: Module foo not found" does not match. A sentence
	// like "modprobe the driver" does.
	reKernelModuleOrMount = regexp.MustCompile(`(?i)(?:\b(?:insmod|modprobe|rmmod)\b\s+[A-Za-z0-9_./-]\S*|\bmount\b[^\n]{0,60}(?:\s-o\s|/dev/|--bind|\btmpfs\b|\bproc\b|\bsysfs\b))`)

	// PE-004: changing the packet filter. Inside a sandbox there is no
	// legitimate reason to rewrite iptables/nftables rules.
	// FP note: low. The rule names are distinctive tokens.
	reFirewallChange = regexp.MustCompile(`(?i)\b(?:iptables|ip6tables|nft|nftables|ufw)\b\s+`)

	// CA-001: reading a process environment block. /proc/<pid>/environ is where
	// injected cloud and CI credentials live, including the agent's own.
	// FP note: low. The path requires a real /proc entry with an environ file.
	reProcEnviron = regexp.MustCompile(`(?i)/proc/[A-Za-z0-9_.-]+/environ\b`)

	// CA-002: sweeping the environment for secrets. "env | grep TOKEN" is a
	// common first step in credential harvesting.
	// FP note: the pipe into grep is required, so plain "env" and "printenv"
	// do not match. A debugging session that greps one variable still matches.
	reEnvGrepSweep = regexp.MustCompile(`(?i)\b(?:env|printenv)\b[^\n]{0,20}\|\s*grep\b`)

	// CA-003: touching a well-known credential store: AWS shared credentials,
	// a netrc, the Docker client config, the opencode agent auth file, the gh
	// CLI hosts file, or the AWS secret key variable name.
	// FP note: a documentation line that names the exact file path will match.
	// Bare words like "credentials" or "netrc" without the path do not.
	reCredentialStores = regexp.MustCompile(`(?i)(?:\.aws/credentials\b|(?:^|[\s/])\.netrc\b|\.docker/config\.json\b|(?:\.config/)?opencode/(?:auth\.json|credentials\.json)\b|\.config/gh/hosts\.yml\b|\bAWS_SECRET_ACCESS_KEY\b)`)

	// CA-004: reading an SSH private key. The .ssh/ or path separator prefix is
	// required so that prose about key filenames does not match.
	// FP note: a matching path to the .pub half also matches; public keys are
	// not secret, so this can be a benign hit on a key listing.
	reSSHPrivateKey = regexp.MustCompile(`(?i)(?:\.ssh/|/)id_(?:rsa|ed25519|ecdsa|dsa)\b`)

	// CA-005: reaching a cloud instance metadata service, which hands out
	// short-lived credentials to anything that can make an HTTP request.
	// FP note: low. These are fixed addresses, not words.
	reCloudMetadata = regexp.MustCompile(`(?i)\b169\.254\.169\.254\b|\bmetadata\.google\.internal\b|\b100\.100\.100\.200\b`)

	// CA-006: running a password cracker. The tool name requires an option
	// argument so that a sentence naming the tool does not match.
	// FP note: "john" is also a common name; the required leading dash keeps
	// ordinary prose out, but a command such as "john --help" will match.
	rePasswordCracker = regexp.MustCompile(`(?i)\b(?:hydra|hashcat|medusa|john)\b\s+-{1,2}\S`)

	// RC-001: active network reconnaissance. Signal: a port scanner, an nc
	// probe sweep (-z), or a shell loop over a seq range, which is the usual
	// hand-rolled port scan.
	// FP note: "nmap" in documentation or a tool listing also matches. The
	// scan-loop alternative is deliberately narrow.
	reNetworkScanner = regexp.MustCompile(`(?i)\b(?:nmap|masscan|zmap)\b|\b(?:nc|ncat|netcat)\b[^\n]{0,30}\s-z\b|\bfor\s+[A-Za-z_]\w*\s+in\s+\$\(\s*seq\b`)

	// RC-002: an offensive exploitation framework. These have no place in a
	// build or test run.
	// FP note: low, though a security test suite that shells out to nuclei or
	// sqlmap against a fixture would match.
	reExploitFramework = regexp.MustCompile(`(?i)\b(?:msfconsole|msfvenom|metasploit|sqlmap|nikto|gobuster|ffuf|enum4linux|wpscan|nuclei)\b`)

	// RC-003: reading the shadow password database, which requires or implies
	// root and yields offline-crackable hashes.
	// FP note: a documentation line that names /etc/shadow matches; the path is
	// distinctive enough that this is the preferred trade-off.
	reShadowRead = regexp.MustCompile(`(?i)/etc/shadow\b`)

	// RC-004: reading the account database. This is a normal system file, so a
	// read verb is required in front of it.
	// FP note: many tools legitimately read /etc/passwd (getent, ls of the
	// file). MEDIUM severity reflects that.
	rePasswdRead = regexp.MustCompile(`(?i)(?:^|[\s;|&])(?:cat|less|more|head|tail|grep|awk|cp|scp|strings|xxd|getent)\b[^\n]{0,60}/etc/passwd\b`)

	// RC-005: enumerating sockets and connections, typically to find a
	// reachable service or a sibling sandbox.
	// FP note: netstat/ss without a socket-selecting flag does not match.
	reSocketSweep = regexp.MustCompile(`(?i)\b(?:ss|netstat)\b\s+-{1,2}[A-Za-z]*[tunlaxp][A-Za-z]*`)

	// RC-006: pulling Kubernetes secrets through the cluster API.
	// FP note: "kubectl get secret" also matches a legitimate deployment read;
	// severity is HIGH because secrets are the target.
	reKubectlSecrets = regexp.MustCompile(`(?i)\bkubectl\b[^\n]{0,80}\bsecrets?\b`)

	// RS-001: a shell handed back over a raw TCP connection with netcat's
	// execute flag.
	// FP note: low. The -e flag on nc/ncat is the defining signal.
	reNetcatExec = regexp.MustCompile(`(?i)\b(?:nc|ncat|netcat)\b[^\n]{0,40}\s-[A-Za-z]*e[A-Za-z]*(?:\s|$)`)

	// RS-002: the bash /dev/tcp reverse shell, in its interactive and its
	// file-descriptor forms.
	// FP note: low. /dev/tcp requires bash and is rarely used benignly, though
	// a network test script may use it deliberately.
	reBashDevTCP = regexp.MustCompile(`(?i)(?:\b(?:bash|sh)\s+-i\b[^\n]{0,40}/dev/tcp/|\bexec\s+\d+\s*<>?\s*/dev/tcp/|\b\d+\s*<>?\s*/dev/tcp/)`)

	// RS-003: socat bridging a socket to a process. This is the
	// dependency-light reverse shell used when netcat lacks -e.
	// FP note: low. socat is also a legitimate port-forwarding tool, so a
	// tunnel setup will match; that is still worth review.
	reSocatExec = regexp.MustCompile(`(?i)\bsocat\b[^\n]{0,80}\bexec:`)

	// RS-004: Python allocating a pseudo-terminal to upgrade a dumb shell.
	// FP note: low. pty.spawn is used by container tooling, but not by builds.
	rePtySpawn = regexp.MustCompile(`(?i)\bpty\.spawn\b`)

	// PS-001: installing a scheduled job. Signal: editing or removing a crontab
	// entry (-e/-r), or writing into the cron spool directories.
	// FP note: "crontab -l" is a read and deliberately does not match.
	reCronPersistence = regexp.MustCompile(`(?i)(?:\bcrontab\b[^\n]{0,20}\s-{1,2}[er]\b|>>?\s*\S*/etc/cron|/etc/cron\.d\b|/var/spool/cron/)`)

	// PS-002: installing a systemd unit or enabling one at boot.
	// FP note: listing /etc/systemd/system is harmless but matches the unit
	// path form; severity is HIGH because the write form dominates.
	reSystemdPersistence = regexp.MustCompile(`(?i)(?:>>?\s*\S*/etc/systemd/system/|/etc/systemd/system/\S*\.service\b|\bsystemctl\s+(?:enable|link|daemon-reload)\b)`)

	// PS-003: appending to a shell startup file so code runs on every login.
	// FP note: the redirection or "tee -a" is required, so a shell profile that
	// merely exists in the output does not match.
	reShellRCPersistence = regexp.MustCompile(`(?i)(?:>>?\s*\S*\.(?:bashrc|bash_profile|profile|zshrc|zprofile)\b|\btee\b[^\n]{0,20}\s-a\b[^\n]{0,60}\.(?:bashrc|bash_profile|zshrc)\b)`)

	// PS-004: adding an SSH key that survives the run.
	// FP note: "authorized_keys" alone does not match; a redirection, a tee, or
	// ssh-copy-id is required.
	reAuthorizedKeys = regexp.MustCompile(`(?i)(?:>>?\s*\S*authorized_keys\b|\bauthorized_keys\b[^\n]{0,40}(?:>>|\btee\b)|\bssh-copy-id\b)`)

	// EX-001: uploading a file to a remote endpoint with curl or wget.
	// FP note: a release pipeline that uploads artifacts will match; in an
	// agent sandbox that is exactly the behaviour worth reviewing.
	reUploadTransfer = regexp.MustCompile(`(?i)\b(?:curl|wget)\b[^\n]{0,120}(?:\s-T\b|--upload-file\b|--post-file\b|--data-binary\s*@|--data\s*@)`)

	// EX-002: base64-encoding a payload and piping it straight into a network
	// client. This is the encode-then-post exfiltration shape.
	// FP note: piping an encoded blob into a client for a legitimate API call
	// also matches; that is rare in build output.
	reBase64ToNetwork = regexp.MustCompile(`(?i)\bbase64\b[^\n]{0,120}\|\s*(?:curl|wget|nc|ncat|netcat|ssh|dig|nslookup)\b`)

	// EX-003: DNS tunnelling, either a dedicated tool or an explicit TXT query
	// piped onward (the usual way to smuggle data past egress filtering).
	// FP note: an ordinary "dig example.com TXT" does not match unless it is
	// piped; the tool names are distinctive.
	reDNSTunnel = regexp.MustCompile(`(?i)\b(?:iodine|dnscat2?|dns2tcp|tuns)\b|\b(?:dig|nslookup)\b[^\n]{0,60}\bTXT\b[^\n]{0,40}\|`)

	// EX-004: copying data to a remote host as another user.
	// FP note: legitimate cross-host deploys use the same shape, so this is
	// MEDIUM. The user@host: form is required, which keeps local copies out.
	reRemoteCopy = regexp.MustCompile(`(?i)\b(?:scp|rsync)\b[^\n]{0,80}\s[A-Za-z0-9._-]+@[A-Za-z0-9._-]+:`)

	// DS-001: recursive forced deletion of the filesystem root or the home
	// root. Subpaths are deliberately excluded: "rm -rf /tmp/build" is normal.
	// FP note: a build script that legitimately wipes its scratch directory
	// under $HOME one level down ("rm -rf ~/work/x") does not match; only the
	// bare root, /, /*, ~, and $HOME do.
	reRootDelete = regexp.MustCompile(`(?i)\brm\b[^\n]{0,40}(?:-{1,2}[A-Za-z]*(?:rf|fr)[A-Za-z]*|--recursive[^\n]{0,20}--force)[^\n]{0,20}\s(?:--no-preserve-root\s+)?(?:/(?:\*|\s|$)|(?:~|\$HOME|\$\{HOME\})(?:\*|\s|$))`)

	// DS-002: creating a filesystem, which destroys whatever was on the device.
	// FP note: low. Requires the mkfs token plus an argument.
	reMkfs = regexp.MustCompile(`(?i)\bmkfs(?:\.[a-z0-9]+)?\b\s+\S`)

	// DS-003: raw-writing to a block device.
	// FP note: low. The of= device path is required.
	reDDToDevice = regexp.MustCompile(`(?i)\bdd\b[^\n]{0,80}\bof=/dev/(?:sd|nvme|vd|hd|mmcblk|disk|loop|mapper|dm-)`)

	// DS-004: the classic fork bomb.
	// FP note: RE2 has no backreferences, so the named-function variant
	// ("bomb(){ bomb|bomb& };bomb") cannot be detected; that is a known
	// false-negative and is recorded here rather than guessed at.
	reForkBomb = regexp.MustCompile(`(?i):\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`)

	// DS-005: shredding files to make recovery harder.
	// FP note: "shred" is also a common English word in prose; an argument is
	// required, which keeps most prose out.
	reShred = regexp.MustCompile(`(?i)\bshred\b\s+(?:-\S+\s+)*\S`)

	// CE-001: touching the Docker socket, which is equivalent to root on the
	// host when mounted into a container.
	// FP note: low. The socket path is specific.
	reDockerSocket = regexp.MustCompile(`(?i)/(?:var/)?run/docker\.sock\b`)

	// CE-002: asking the runtime for host-level privilege: privileged mode,
	// SYS_ADMIN or ALL capabilities, or a host PID/network namespace.
	// FP note: a documentation quote of a docker run line matches; the flags
	// themselves are not ambiguous.
	reHostPrivilegeFlags = regexp.MustCompile(`(?i)(?:--privileged\b|--cap-add\s*=?\s*(?:SYS_ADMIN|ALL)\b|--pid\s*=?\s*host\b|--net(?:work)?\s*=?\s*host\b)`)

	// CE-003: entering another namespace set on the host.
	// FP note: low. nsenter is a container-escape primitive with no build use.
	reNsenter = regexp.MustCompile(`(?i)\bnsenter\b`)

	// CE-004: writing a cgroup release_agent, the classic cgroup v1 escape that
	// makes the host run an attacker-supplied binary.
	// FP note: low. The kernel interface name is unique.
	reReleaseAgent = regexp.MustCompile(`(?i)\brelease_agent\b`)

	// CM-001: cryptocurrency mining, by miner binary or by pool protocol.
	// FP note: low. Miners and stratum URLs do not appear in build output.
	reCryptominer = regexp.MustCompile(`(?i)\b(?:xmrig|minerd|cpuminer|ccminer|cgminer|xmr-stak)\b|stratum\+(?:tcp|ssl|tls)://`)

	// BG-001: a patch that rewrites the build or test gate itself. This is a
	// patch-stream signal: the header of a diff that touches build.sh or
	// test.sh, which is how a run would neuter its own verification.
	// FP note: an honest fix to build.sh trips this too; that is intentional,
	// because the gate change is what needs review.
	rePatchTouchesGate = regexp.MustCompile(`(?i)^(?:diff\s+--git|(?:---|\+\+\+))\s+.*[\s/](?:build|test)\.sh(?:[\s\r]|$)`)

	// BG-002: a patch that rewrites adjacent gate machinery: the Makefile, CI
	// workflow files, or a justfile.
	// FP note: same shape as BG-001, one step less direct; MEDIUM severity.
	rePatchTouchesCI = regexp.MustCompile(`(?i)^(?:diff\s+--git|(?:---|\+\+\+))\s+.*[\s/](?:(?:Makefile|justfile)(?:[\s\r]|$)|\.github/workflows/\S+)`)
)

// defaultRules builds a fresh slice for every call so that callers cannot
// mutate the shipped ruleset.
func defaultRules() []Rule {
	return []Rule{
		// --- privilege escalation -------------------------------------------
		{
			ID:          "PE-001",
			Category:    "privilege-escalation",
			Description: "Write to /etc/sudoers, granting durable root via sudo",
			Severity:    SeverityCritical,
			Pattern:     reSudoersWrite,
		},
		{
			ID:          "PE-002",
			Category:    "privilege-escalation",
			Description: "Grant elevated privilege to a file: setuid bit, setcap capability, or chown to root",
			Severity:    SeverityHigh,
			Pattern:     rePrivilegeBits,
		},
		{
			ID:          "PE-003",
			Category:    "privilege-escalation",
			Description: "Load a kernel module or mount a device or kernel filesystem",
			Severity:    SeverityHigh,
			Pattern:     reKernelModuleOrMount,
		},
		{
			ID:          "PE-004",
			Category:    "privilege-escalation",
			Description: "Modify the packet filter with iptables, nftables, or ufw",
			Severity:    SeverityHigh,
			Pattern:     reFirewallChange,
		},

		// --- credential access ----------------------------------------------
		{
			ID:          "CA-001",
			Category:    "credential-access",
			Description: "Read a process environment block from /proc/<pid>/environ",
			Severity:    SeverityHigh,
			Pattern:     reProcEnviron,
		},
		{
			ID:          "CA-002",
			Category:    "credential-access",
			Description: "Sweep the environment for secrets with env or printenv piped into grep",
			Severity:    SeverityMedium,
			Pattern:     reEnvGrepSweep,
		},
		{
			ID:          "CA-003",
			Category:    "credential-access",
			Description: "Touch a credential store: AWS shared credentials, netrc, Docker or opencode config, gh hosts",
			Severity:    SeverityHigh,
			Pattern:     reCredentialStores,
		},
		{
			ID:          "CA-004",
			Category:    "credential-access",
			Description: "Reference an SSH private key under .ssh",
			Severity:    SeverityHigh,
			Pattern:     reSSHPrivateKey,
		},
		{
			ID:          "CA-005",
			Category:    "credential-access",
			Description: "Reach a cloud instance metadata endpoint for credentials",
			Severity:    SeverityCritical,
			Pattern:     reCloudMetadata,
		},
		{
			ID:          "CA-006",
			Category:    "credential-access",
			Description: "Run a password cracking tool against captured hashes",
			Severity:    SeverityHigh,
			Pattern:     rePasswordCracker,
		},

		// --- recon ----------------------------------------------------------
		{
			ID:          "RC-001",
			Category:    "recon",
			Description: "Run a port scanner or a shell loop that sweeps a port range",
			Severity:    SeverityHigh,
			Pattern:     reNetworkScanner,
		},
		{
			ID:          "RC-002",
			Category:    "recon",
			Description: "Invoke an offensive exploitation or web scanning framework",
			Severity:    SeverityHigh,
			Pattern:     reExploitFramework,
		},
		{
			ID:          "RC-003",
			Category:    "recon",
			Description: "Read the shadow password database /etc/shadow",
			Severity:    SeverityHigh,
			Pattern:     reShadowRead,
		},
		{
			ID:          "RC-004",
			Category:    "recon",
			Description: "Read the account database /etc/passwd with a file reader",
			Severity:    SeverityMedium,
			Pattern:     rePasswdRead,
		},
		{
			ID:          "RC-005",
			Category:    "recon",
			Description: "Enumerate sockets and connections with ss or netstat",
			Severity:    SeverityMedium,
			Pattern:     reSocketSweep,
		},
		{
			ID:          "RC-006",
			Category:    "recon",
			Description: "Read Kubernetes secrets through kubectl",
			Severity:    SeverityHigh,
			Pattern:     reKubectlSecrets,
		},

		// --- reverse shell --------------------------------------------------
		{
			ID:          "RS-001",
			Category:    "reverse-shell",
			Description: "Hand a shell to a remote peer with nc/ncat -e",
			Severity:    SeverityCritical,
			Pattern:     reNetcatExec,
		},
		{
			ID:          "RS-002",
			Category:    "reverse-shell",
			Description: "Open an interactive shell or file descriptor over bash /dev/tcp",
			Severity:    SeverityCritical,
			Pattern:     reBashDevTCP,
		},
		{
			ID:          "RS-003",
			Category:    "reverse-shell",
			Description: "Bridge a socket to a process with socat exec",
			Severity:    SeverityCritical,
			Pattern:     reSocatExec,
		},
		{
			ID:          "RS-004",
			Category:    "reverse-shell",
			Description: "Allocate a pseudo-terminal with Python pty.spawn to upgrade a shell",
			Severity:    SeverityCritical,
			Pattern:     rePtySpawn,
		},

		// --- persistence ----------------------------------------------------
		{
			ID:          "PS-001",
			Category:    "persistence",
			Description: "Install or alter a scheduled job through crontab or the cron spool",
			Severity:    SeverityHigh,
			Pattern:     reCronPersistence,
		},
		{
			ID:          "PS-002",
			Category:    "persistence",
			Description: "Create or enable a systemd unit so code runs at boot",
			Severity:    SeverityHigh,
			Pattern:     reSystemdPersistence,
		},
		{
			ID:          "PS-003",
			Category:    "persistence",
			Description: "Append to a shell startup file such as .bashrc or .profile",
			Severity:    SeverityHigh,
			Pattern:     reShellRCPersistence,
		},
		{
			ID:          "PS-004",
			Category:    "persistence",
			Description: "Add an SSH key to authorized_keys or via ssh-copy-id",
			Severity:    SeverityHigh,
			Pattern:     reAuthorizedKeys,
		},

		// --- exfiltration ---------------------------------------------------
		{
			ID:          "EX-001",
			Category:    "exfiltration",
			Description: "Upload a file to a remote endpoint with curl or wget",
			Severity:    SeverityHigh,
			Pattern:     reUploadTransfer,
		},
		{
			ID:          "EX-002",
			Category:    "exfiltration",
			Description: "Base64-encode a payload and pipe it into a network client",
			Severity:    SeverityHigh,
			Pattern:     reBase64ToNetwork,
		},
		{
			ID:          "EX-003",
			Category:    "exfiltration",
			Description: "Tunnel data over DNS with a tunnelling tool or a piped TXT query",
			Severity:    SeverityHigh,
			Pattern:     reDNSTunnel,
		},
		{
			ID:          "EX-004",
			Category:    "exfiltration",
			Description: "Copy files to a remote user@host with scp or rsync",
			Severity:    SeverityMedium,
			Pattern:     reRemoteCopy,
		},

		// --- destructive ----------------------------------------------------
		{
			ID:          "DS-001",
			Category:    "destructive",
			Description: "Recursively force-delete the filesystem root or home root",
			Severity:    SeverityCritical,
			Pattern:     reRootDelete,
		},
		{
			ID:          "DS-002",
			Category:    "destructive",
			Description: "Create a filesystem over a device with mkfs",
			Severity:    SeverityCritical,
			Pattern:     reMkfs,
		},
		{
			ID:          "DS-003",
			Category:    "destructive",
			Description: "Write raw data to a block device with dd",
			Severity:    SeverityCritical,
			Pattern:     reDDToDevice,
		},
		{
			ID:          "DS-004",
			Category:    "destructive",
			Description: "Exhaust the process table with a fork bomb",
			Severity:    SeverityCritical,
			Pattern:     reForkBomb,
		},
		{
			ID:          "DS-005",
			Category:    "destructive",
			Description: "Destroy file contents beyond recovery with shred",
			Severity:    SeverityHigh,
			Pattern:     reShred,
		},

		// --- container escape -----------------------------------------------
		{
			ID:          "CE-001",
			Category:    "container-escape",
			Description: "Access the Docker socket, which controls the host daemon",
			Severity:    SeverityCritical,
			Pattern:     reDockerSocket,
		},
		{
			ID:          "CE-002",
			Category:    "container-escape",
			Description: "Request host-level privilege: --privileged, SYS_ADMIN, or host PID/network namespace",
			Severity:    SeverityHigh,
			Pattern:     reHostPrivilegeFlags,
		},
		{
			ID:          "CE-003",
			Category:    "container-escape",
			Description: "Enter host namespaces with nsenter",
			Severity:    SeverityCritical,
			Pattern:     reNsenter,
		},
		{
			ID:          "CE-004",
			Category:    "container-escape",
			Description: "Write a cgroup release_agent to run a binary on the host",
			Severity:    SeverityCritical,
			Pattern:     reReleaseAgent,
		},

		// --- cryptomining ---------------------------------------------------
		{
			ID:          "CM-001",
			Category:    "cryptomining",
			Description: "Run a cryptocurrency miner or connect to a mining pool with stratum",
			Severity:    SeverityCritical,
			Pattern:     reCryptominer,
		},

		// --- build gate tamper ----------------------------------------------
		{
			ID:          "BG-001",
			Category:    "build-gate-tamper",
			Description: "Patch modifies the build.sh or test.sh verification gate",
			Severity:    SeverityHigh,
			Pattern:     rePatchTouchesGate,
		},
		{
			ID:          "BG-002",
			Category:    "build-gate-tamper",
			Description: "Patch modifies adjacent gate machinery: Makefile, justfile, or a CI workflow",
			Severity:    SeverityMedium,
			Pattern:     rePatchTouchesCI,
		},
	}
}

package securityassessment

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mitkox/esf/internal/sandbox"
)

const CandidateSchema = "esf.security-candidate/v1"

const maxCandidateInventoryBytes int64 = 2 << 20

type CandidatePolicy struct {
	MaxFiles         int      `json:"max_files"`
	MaxBytes         int64    `json:"max_bytes"`
	MaxFileBytes     int64    `json:"max_file_bytes"`
	ExcludedPrefixes []string `json:"excluded_prefixes,omitempty"`
}

func DefaultCandidatePolicy() CandidatePolicy {
	return CandidatePolicy{MaxFiles: 4096, MaxBytes: 64 << 20, MaxFileBytes: 8 << 20}
}

func (p CandidatePolicy) validate() error {
	if p.MaxFiles <= 0 || p.MaxFiles > 4096 || p.MaxBytes <= 0 || p.MaxBytes > 64<<20 || p.MaxFileBytes <= 0 || p.MaxFileBytes > p.MaxBytes {
		return errors.New("invalid bounded candidate policy")
	}
	for _, prefix := range p.ExcludedPrefixes {
		if !safeCandidatePath(prefix) {
			return errors.New("invalid candidate exclusion prefix")
		}
	}
	return nil
}

func (p CandidatePolicy) archiveLimit() int64 {
	return p.MaxBytes + int64(p.MaxFiles)*2048 + 10240
}

type CandidateEntry struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Mode     int64  `json:"mode"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256,omitempty"`
	Excluded string `json:"excluded,omitempty"`
}

// CandidateManifest and CandidateExport contain private source inventory. Only
// their digests belong in the ordinary assessment report or repair summary.
type CandidateManifest struct {
	Schema        string           `json:"schema"`
	Digest        string           `json:"digest"`
	ArchiveSHA256 string           `json:"archive_sha256"`
	Entries       []CandidateEntry `json:"entries"`
}

type CandidateExport struct {
	Manifest CandidateManifest `json:"manifest"`
	Files    map[string][]byte `json:"-"`
	Archive  []byte            `json:"-"`
}

func safeCandidatePath(name string) bool {
	return safeEvidenceName(name) && utf8.ValidString(name) && !strings.ContainsAny(name, "\t")
}

func candidateDigest(m CandidateManifest) string {
	m.Digest = ""
	data, _ := json.Marshal(m)
	return SHA256(data)
}

func VerifyCandidateUnchanged(before, after CandidateManifest) error {
	if before.Schema != CandidateSchema || after.Schema != CandidateSchema || before.Digest == "" || after.Digest == "" ||
		candidateDigest(before) != before.Digest || candidateDigest(after) != after.Digest || before.Digest != after.Digest {
		return errors.New("candidate changed between verification and assessment")
	}
	return nil
}

func candidateFromArchive(m CandidateManifest, archive []byte, policy CandidatePolicy) (CandidateExport, error) {
	if err := policy.validate(); err != nil {
		return CandidateExport{}, err
	}
	if m.Schema != CandidateSchema || len(m.Entries) > policy.MaxFiles || int64(len(archive)) > policy.archiveLimit() {
		return CandidateExport{}, errors.New("invalid or oversized candidate inventory/archive")
	}
	expected := map[string]CandidateEntry{}
	var total int64
	previous := ""
	for _, entry := range m.Entries {
		if !safeCandidatePath(entry.Path) || entry.Path <= previous || entry.Mode < 0 || entry.Mode > 0777 || entry.Size < 0 || entry.Size > policy.MaxFileBytes {
			return CandidateExport{}, errors.New("invalid candidate entry")
		}
		previous = entry.Path
		switch entry.Kind {
		case "file":
			if !digestPattern.MatchString(entry.SHA256) {
				return CandidateExport{}, errors.New("candidate file digest missing")
			}
			total += entry.Size
		case "directory":
			if entry.Size != 0 {
				return CandidateExport{}, errors.New("invalid candidate directory")
			}
		case "symlink":
			if entry.Excluded != "symlink" || !digestPattern.MatchString(entry.SHA256) {
				return CandidateExport{}, errors.New("candidate symlink cannot be exported")
			}
		default:
			return CandidateExport{}, errors.New("unsupported candidate file kind")
		}
		if total > policy.MaxBytes {
			return CandidateExport{}, errors.New("candidate exceeds byte limit")
		}
		if entry.Excluded == "" {
			expected[entry.Path] = entry
		}
	}
	export := CandidateExport{Manifest: m, Archive: append([]byte(nil), archive...), Files: map[string][]byte{}}
	reader := tar.NewReader(bytes.NewReader(archive))
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return CandidateExport{}, errors.New("invalid candidate archive")
		}
		name := strings.TrimSuffix(header.Name, "/")
		entry, ok := expected[name]
		if !ok || seen[name] || !safeCandidatePath(name) || header.Mode != entry.Mode || header.Size != entry.Size {
			return CandidateExport{}, errors.New("candidate archive does not match inventory")
		}
		seen[name] = true
		if entry.Kind == "directory" && header.Typeflag == tar.TypeDir {
			continue
		}
		if entry.Kind != "file" || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) {
			return CandidateExport{}, errors.New("candidate archive contains non-regular input")
		}
		data, err := io.ReadAll(io.LimitReader(reader, policy.MaxFileBytes+1))
		if err != nil || int64(len(data)) != entry.Size || SHA256(data) != entry.SHA256 {
			return CandidateExport{}, errors.New("candidate bytes changed or truncated")
		}
		export.Files[name] = data
	}
	if len(seen) != len(expected) {
		return CandidateExport{}, errors.New("candidate archive omits declared input")
	}
	export.Manifest.ArchiveSHA256 = SHA256(archive)
	export.Manifest.Digest = candidateDigest(export.Manifest)
	return export, nil
}

// ExportCandidate freezes the filesystem inside Cube and transfers only that
// bounded archive via BoundedFileReader. The isolated Python scanner uses descriptor-
// relative no-follow opens; symlinks never cause reads outside the source tree.
func ExportCandidate(ctx context.Context, sb sandbox.Sandbox, root string, policy CandidatePolicy) (result CandidateExport, returnErr error) {
	if err := policy.validate(); err != nil {
		return CandidateExport{}, err
	}
	reader, ok := sb.(sandbox.BoundedFileReader)
	if !ok {
		return CandidateExport{}, errors.New("candidate export requires bounded sandbox file transfer")
	}
	if !path.IsAbs(root) || path.Clean(root) != root || root == "/" || strings.ContainsAny(root, "\x00\r\n") {
		return CandidateExport{}, errors.New("candidate root must be a clean absolute application directory")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return CandidateExport{}, err
	}
	archivePath := "/tmp/.esf-security-candidate-" + hex.EncodeToString(nonce) + ".tar"
	inventoryPath := archivePath + ".json"
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		out, err := sb.Execute(cleanupCtx, sandbox.Command{Argv: []string{"rm", "-f", "--", archivePath, inventoryPath}, Description: "remove private candidate export"})
		if err != nil || !out.Succeeded() {
			returnErr = errors.Join(returnErr, errors.New("private candidate export cleanup incomplete"))
		}
	}()
	policyJSON, _ := json.Marshal(policy)
	out, err := sb.Execute(ctx, sandbox.Command{Argv: []string{"python3", "-I", "-c", candidateSnapshotScript, root, archivePath, inventoryPath, string(policyJSON)}, Dir: "/", Description: "freeze exact candidate bytes"})
	if err != nil || !out.Succeeded() {
		return CandidateExport{}, errors.New("candidate snapshot failed or inventory truncated")
	}
	inventoryData, err := reader.ReadFileBounded(ctx, inventoryPath, maxCandidateInventoryBytes)
	if err != nil || int64(len(inventoryData)) > maxCandidateInventoryBytes {
		return CandidateExport{}, errors.New("candidate snapshot inventory transfer failed")
	}
	var inventory struct {
		Manifest      CandidateManifest `json:"manifest"`
		ArchiveSHA256 string            `json:"archive_sha256"`
	}
	if json.Unmarshal(inventoryData, &inventory) != nil || !digestPattern.MatchString(inventory.ArchiveSHA256) {
		return CandidateExport{}, errors.New("invalid candidate snapshot inventory")
	}
	archive, err := reader.ReadFileBounded(ctx, archivePath, policy.archiveLimit())
	if err != nil || int64(len(archive)) > policy.archiveLimit() {
		return CandidateExport{}, errors.New("candidate snapshot transfer failed")
	}
	if err := VerifyEvidence(archive, inventory.ArchiveSHA256); err != nil {
		return CandidateExport{}, errors.New("candidate snapshot transfer digest mismatch")
	}
	result, err = candidateFromArchive(inventory.Manifest, archive, policy)
	if err != nil {
		return CandidateExport{}, fmt.Errorf("candidate export: %w", err)
	}
	return result, nil
}

const candidateSnapshotScript = `
import hashlib, io, json, os, stat, sys, tarfile
root, destination, inventory_destination, policy_text = sys.argv[1:]
policy = json.loads(policy_text)
entries, payloads = [], {}
total = 0
def excluded(relative, name):
    if name == '.git': return 'repository-metadata'
    if name in ('.aws', '.ssh', '.env', 'id_rsa', 'id_ed25519', 'credentials.json') or name.startswith('.env.') or name.endswith(('.key', '.pem')): return 'credential-path'
    if any(relative == p or relative.startswith(p + '/') for p in policy.get('excluded_prefixes', [])): return 'operator-exclusion'
    return ''
def visit(directory, prefix):
    global total
    for name in sorted(os.listdir(directory)):
        relative = prefix + name
        if len(entries) >= policy['max_files']: raise ValueError('candidate file limit')
        observed = os.stat(name, dir_fd=directory, follow_symlinks=False)
        mode = stat.S_IMODE(observed.st_mode)
        if mode & 0o7000: raise ValueError('unsupported file mode')
        reason = excluded(relative, name)
        entry = dict(path=relative, mode=mode, size=0)
        if stat.S_ISLNK(observed.st_mode):
            link = os.readlink(name, dir_fd=directory).encode('utf-8')
            entry.update(kind='symlink', size=len(link), sha256=hashlib.sha256(link).hexdigest(), excluded='symlink')
        elif stat.S_ISDIR(observed.st_mode):
            entry.update(kind='directory')
            if reason: entry['excluded'] = reason
            entries.append(entry)
            if not reason:
                child = os.open(name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
                try: visit(child, relative + '/')
                finally: os.close(child)
            continue
        elif stat.S_ISREG(observed.st_mode):
            if observed.st_size > policy['max_file_bytes']: raise ValueError('candidate file too large')
            descriptor = os.open(name, os.O_RDONLY | os.O_NOFOLLOW, dir_fd=directory)
            with os.fdopen(descriptor, 'rb') as stream:
                before = os.fstat(stream.fileno())
                data = stream.read(policy['max_file_bytes'] + 1)
                after = os.fstat(stream.fileno())
            if len(data) > policy['max_file_bytes'] or before.st_size != len(data) or (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns): raise ValueError('candidate changed while reading')
            total += len(data)
            if total > policy['max_bytes']: raise ValueError('candidate byte limit')
            if b'-----BEGIN ' in data and b'PRIVATE KEY-----' in data: reason = 'private-key-content'
            entry.update(kind='file', size=len(data), sha256=hashlib.sha256(data).hexdigest())
            if reason: entry['excluded'] = reason
            else: payloads[relative] = data
        else: raise ValueError('unsupported special file')
        entries.append(entry)
root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
try: visit(root_fd, '')
finally: os.close(root_fd)
entries.sort(key=lambda entry: entry['path'])
try:
    descriptor = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        with tarfile.open(fileobj=output, mode='w', format=tarfile.PAX_FORMAT) as archive:
            for entry in entries:
                if entry.get('excluded'): continue
                info = tarfile.TarInfo(entry['path'])
                info.mode = entry['mode']
                info.size = entry['size']
                if entry['kind'] == 'directory': info.type = tarfile.DIRTYPE
                archive.addfile(info, io.BytesIO(payloads[entry['path']]) if entry['kind'] == 'file' else None)
        output.flush()
        os.fsync(output.fileno())
    with open(destination, 'rb') as source: archive_digest = hashlib.sha256(source.read()).hexdigest()
    inventory = json.dumps(dict(manifest=dict(schema='esf.security-candidate/v1', entries=entries), archive_sha256=archive_digest), sort_keys=True).encode('utf-8')
    descriptor = os.open(inventory_destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'wb') as output:
        output.write(inventory)
        output.flush()
        os.fsync(output.fileno())
except BaseException:
    try: os.unlink(destination)
    except FileNotFoundError: pass
    raise
`

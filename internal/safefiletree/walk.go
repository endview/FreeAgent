// Package safefiletree walks a file tree without reopening descendants by
// absolute or root-relative path. Every child is opened relative to a held
// parent directory handle, and links, reparse points, filesystem boundaries,
// and multiply-linked regular files are rejected before their contents are
// exposed to a visitor.
package safefiletree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// ErrUnsafeTree reports that a tree could not be traversed without following
// a link or accepting an unstable or unsupported filesystem object.
var ErrUnsafeTree = errors.New("safefiletree: unsafe file tree")

// ErrEntryExpired reports use of an Entry capability after its visitor
// callback returned.
var ErrEntryExpired = errors.New("safefiletree: entry capability expired")

// ErrSyncUnavailable reports that WalkWithOptions was not asked to open
// regular files with the additional access required to flush them.
var ErrSyncUnavailable = errors.New("safefiletree: sync capability unavailable")

// ErrMetadataControlUnavailable reports that WalkWithOptions was not asked to
// open entries with the additional access required for metadata mutation.
var ErrMetadataControlUnavailable = errors.New("safefiletree: metadata control capability unavailable")

// ErrEntryLimit reports that enumeration exceeded a configured global or
// per-directory entry ceiling before the directory listing could be accepted.
var ErrEntryLimit = errors.New("safefiletree: directory entry limit exceeded")

const (
	// DefaultMaxEntries bounds the total number of descendant directory entries
	// that one walk may enumerate. The root itself is not counted.
	DefaultMaxEntries = 65_536
	// DefaultMaxEntriesPerDirectory bounds the entries retained for lexical
	// ordering from any one opened directory.
	DefaultMaxEntriesPerDirectory = 32_768

	directoryReadChunk = 256
)

// Kind is the verified kind of an opened tree entry.
type Kind uint8

const (
	KindDirectory Kind = iota + 1
	KindRegularFile
)

// Entry describes an object that is still held open by Walk. Path is "." for
// the root and otherwise a slash-separated relative path. Reader is non-nil
// only for a regular file, starts at offset zero, and is valid only until the
// visitor returns. A visitor must not retain Reader after returning.
type Entry struct {
	Path   string
	Kind   Kind
	Info   fs.FileInfo
	Reader io.Reader
	access *entryAccess
}

// Sync flushes the already-opened entry handle. It is valid only during the
// callback that received the Entry. Callers that need to sync regular files
// must use WalkWithOptions with OpenForSync set. On Windows, directory Sync is
// a no-op because the platform has no portable directory-fsync operation.
func (entry Entry) Sync() error {
	if entry.access == nil {
		return ErrEntryExpired
	}
	return entry.access.sync()
}

// Control invokes control with the platform descriptor for the already-opened
// entry. It is valid only during the callback that received the Entry. control
// must not close, duplicate, retain, or use the descriptor after it returns.
// Control exists for narrow handle-based metadata checks which cannot be
// expressed through fs.FileInfo.
func (entry Entry) Control(control func(uintptr) error) error {
	if entry.access == nil {
		return ErrEntryExpired
	}
	return entry.access.control(control)
}

// ControlMetadata invokes control with the platform descriptor for the
// already-opened entry and then refreshes only the platform change timestamp
// that an expected permission or ACL update may alter. The opened object's
// identity, kind, link/reparse safety, filesystem, size, and modification time
// must remain unchanged. control must only update permission or ACL metadata;
// it must not close, duplicate, retain, or otherwise use the descriptor after
// it returns.
//
// ControlMetadata is valid only during the callback that received Entry and
// only when WalkWithOptions set OpenForMetadataControl.
func (entry Entry) ControlMetadata(control func(uintptr) error) error {
	if entry.access == nil {
		return ErrEntryExpired
	}
	return entry.access.controlMetadata(control)
}

// VisitFunc inspects one already-opened entry. Returning an error stops Walk;
// the error remains available through errors.Is/errors.As.
type VisitFunc func(context.Context, Entry) error

// Options configures the callback form of Walk. Visit is called in lexical
// preorder. LeaveDirectory is called after every descendant and therefore in
// child-before-parent order. At least one callback must be non-nil.
//
// OpenForMetadataControl requests the additional platform access used only by
// Entry.ControlMetadata. MaxEntries is the total number of descendant entries
// that may be enumerated during the walk. MaxEntriesPerDirectory is the number
// that may be retained from one directory for lexical ordering. Zero selects
// the corresponding safe default; negative values are invalid.
type Options struct {
	Visit                  VisitFunc
	LeaveDirectory         VisitFunc
	OpenForSync            bool
	OpenForMetadataControl bool
	MaxEntries             int
	MaxEntriesPerDirectory int
}

type entryLimits struct {
	maxEntries             int
	maxEntriesPerDirectory int
}

type walkHooks struct {
	beforeOpen func(string) error
}

type entryAccess struct {
	mu              sync.Mutex
	node            *platformNode
	active          bool
	syncAllowed     bool
	metadataAllowed bool
}

func (access *entryAccess) expire() {
	access.mu.Lock()
	access.active = false
	access.mu.Unlock()
}

func (access *entryAccess) read(buffer []byte) (int, error) {
	access.mu.Lock()
	defer access.mu.Unlock()
	if !access.active {
		return 0, ErrEntryExpired
	}
	return access.node.file.Read(buffer)
}

func (access *entryAccess) sync() error {
	access.mu.Lock()
	defer access.mu.Unlock()
	if !access.active {
		return ErrEntryExpired
	}
	if !access.syncAllowed {
		return ErrSyncUnavailable
	}
	return syncPlatformNode(access.node)
}

func (access *entryAccess) control(control func(uintptr) error) error {
	access.mu.Lock()
	defer access.mu.Unlock()
	if !access.active {
		return ErrEntryExpired
	}
	if control == nil {
		return errors.New("safefiletree: descriptor control is nil")
	}
	raw, err := access.node.file.SyscallConn()
	if err != nil {
		return err
	}
	var controlErr error
	if err := raw.Control(func(descriptor uintptr) {
		controlErr = control(descriptor)
	}); err != nil {
		return err
	}
	return controlErr
}

func (access *entryAccess) controlMetadata(control func(uintptr) error) error {
	access.mu.Lock()
	defer access.mu.Unlock()
	if !access.active {
		return ErrEntryExpired
	}
	if !access.metadataAllowed {
		return ErrMetadataControlUnavailable
	}
	if control == nil {
		return errors.New("safefiletree: metadata descriptor control is nil")
	}
	raw, err := access.node.file.SyscallConn()
	if err != nil {
		return err
	}
	var controlErr error
	if err := raw.Control(func(descriptor uintptr) {
		controlErr = control(descriptor)
	}); err != nil {
		return err
	}
	if controlErr != nil {
		return controlErr
	}
	if err := refreshPlatformNodeMetadata(access.node); err != nil {
		return fmt.Errorf("%w: metadata control changed protected entry state: %v", ErrUnsafeTree, err)
	}
	return nil
}

type borrowedReader struct {
	access *entryAccess
}

func (reader borrowedReader) Read(buffer []byte) (int, error) {
	return reader.access.read(buffer)
}

type walkFrame struct {
	node    *platformNode
	path    string
	entries []os.DirEntry
	next    int
}

// Walk visits root and every descendant in lexical, depth-first order. It
// opens each descendant by basename relative to the already-held parent
// directory handle. It never follows a symbolic link or Windows reparse point.
// All descendants must be on the root object's filesystem, and every regular
// file must have exactly one hard link.
//
// Walk pins and reverifies the opened root object; it does not re-resolve the
// caller's root pathname before returning. A caller that needs proof that the
// pathname still names that object must perform a separate identity check.
func Walk(ctx context.Context, root string, visit VisitFunc) error {
	return WalkWithOptions(ctx, root, Options{Visit: visit})
}

// WalkWithOptions is the callback-configurable form of Walk.
func WalkWithOptions(ctx context.Context, root string, options Options) error {
	return walk(ctx, root, options, walkHooks{})
}

// WalkPath visits the root and then each component of one slash-separated
// relative path. It does not visit siblings. Before opening a component it
// enumerates the held parent to require one exact, case-preserving basename
// match, then opens that basename relative to the same parent handle. All
// opened handles are reverified before the function returns. Like Walk, it
// does not re-resolve the root pathname as a final namespace-identity check.
func WalkPath(
	ctx context.Context,
	root string,
	relative string,
	visit VisitFunc,
) (returnErr error) {
	return WalkPathWithOptions(ctx, root, relative, Options{Visit: visit})
}

// WalkPathWithOptions is the entry-bounded form of WalkPath. MaxEntries
// counts all siblings enumerated while matching held path components, not
// only the components delivered to Visit. LeaveDirectory and OpenForSync are
// unsupported for targeted walks.
func WalkPathWithOptions(
	ctx context.Context,
	root string,
	relative string,
	options Options,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrUnsafeTree)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if root == "" || relative == "." || !fs.ValidPath(relative) || options.Visit == nil ||
		options.LeaveDirectory != nil || options.OpenForSync || options.OpenForMetadataControl {
		return fmt.Errorf("%w: invalid targeted walk input", ErrUnsafeTree)
	}
	limits, err := resolveEntryLimits(options)
	if err != nil {
		return err
	}
	components := strings.Split(relative, "/")
	for _, component := range components {
		if !validChildName(component) {
			return fmt.Errorf("%w: invalid targeted path component", ErrUnsafeTree)
		}
	}

	rootNode, err := openPlatformRoot(root, false)
	if err != nil {
		return unsafeError(".", "open root", err)
	}
	type heldNode struct {
		node *platformNode
		path string
	}
	held := []heldNode{{node: rootNode, path: "."}}
	defer func() {
		for index := len(held) - 1; index >= 0; index-- {
			if verifyErr := verifyPlatformNode(held[index].node, rootNode.device); verifyErr != nil {
				returnErr = errors.Join(
					returnErr,
					unsafeError(held[index].path, "targeted entry changed", verifyErr),
				)
			}
			if closeErr := held[index].node.file.Close(); closeErr != nil {
				returnErr = errors.Join(
					returnErr,
					unsafeError(held[index].path, "close targeted entry", closeErr),
				)
			}
		}
	}()

	if err := callVisitor(ctx, options.Visit, ".", rootNode, false, false); err != nil {
		return err
	}
	parent := rootNode
	currentPath := ""
	enumerated := 0
	for index, component := range components {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := requireExactChildName(parent, component, limits, enumerated)
		if err != nil {
			return unsafeError(
				path.Join(currentPath, component),
				"match targeted child basename",
				err,
			)
		}
		enumerated += entries
		child, err := openPlatformChild(parent, component, rootNode.device, false, false)
		if err != nil {
			return unsafeError(path.Join(currentPath, component), "open targeted child", err)
		}
		currentPath = path.Join(currentPath, component)
		held = append(held, heldNode{node: child, path: currentPath})
		if index != len(components)-1 && child.kind != KindDirectory {
			return unsafeError(currentPath, "targeted path traverses a non-directory", nil)
		}
		if err := callVisitor(ctx, options.Visit, currentPath, child, false, false); err != nil {
			return err
		}
		parent = child
	}
	return ctx.Err()
}

func requireExactChildName(
	parent *platformNode,
	expected string,
	limits entryLimits,
	enumerated int,
) (int, error) {
	entries, err := readDirectoryBounded(parent, limits, enumerated)
	if err != nil {
		return 0, err
	}
	matches := 0
	for _, entry := range entries {
		if entry.Name() == expected {
			matches++
		}
	}
	if matches != 1 {
		return 0, fmt.Errorf("basename %q has %d exact matches", expected, matches)
	}
	return len(entries), nil
}

func walk(
	ctx context.Context,
	root string,
	options Options,
	hooks walkHooks,
) (returnErr error) {
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrUnsafeTree)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if root == "" {
		return fmt.Errorf("%w: root is empty", ErrUnsafeTree)
	}
	if options.Visit == nil && options.LeaveDirectory == nil {
		return fmt.Errorf("%w: callbacks are nil", ErrUnsafeTree)
	}
	limits, err := resolveEntryLimits(options)
	if err != nil {
		return err
	}

	rootNode, err := openPlatformRoot(root, options.OpenForMetadataControl)
	if err != nil {
		return unsafeError(".", "open root", err)
	}
	frames := []walkFrame{{node: rootNode, path: "."}}
	defer func() {
		for index := len(frames) - 1; index >= 0; index-- {
			if verifyErr := verifyPlatformNode(frames[index].node, rootNode.device); verifyErr != nil {
				returnErr = errors.Join(
					returnErr,
					unsafeError(frames[index].path, "entry changed before abort", verifyErr),
				)
			}
			if closeErr := frames[index].node.file.Close(); closeErr != nil {
				returnErr = errors.Join(
					returnErr,
					unsafeError(frames[index].path, "close entry", closeErr),
				)
			}
		}
	}()

	if rootNode.kind != KindDirectory {
		return unsafeError(".", "root is not a directory", nil)
	}
	if err := callVisitor(
		ctx, options.Visit, ".", rootNode,
		options.OpenForSync, options.OpenForMetadataControl,
	); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	enumerated := 0
	entries, err := readDirectoryBounded(rootNode, limits, enumerated)
	if err != nil {
		return unsafeError(".", "enumerate directory", err)
	}
	enumerated += len(entries)
	frames[0].entries = entries

	for len(frames) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		frameIndex := len(frames) - 1
		frame := &frames[frameIndex]
		if frame.next == len(frame.entries) {
			if err := callDirectoryLeave(
				ctx,
				options.LeaveDirectory,
				frame.path,
				frame.node,
				options.OpenForSync,
				options.OpenForMetadataControl,
			); err != nil {
				return err
			}
			if err := verifyPlatformNode(frame.node, rootNode.device); err != nil {
				return unsafeError(frame.path, "directory changed", err)
			}
			node := frame.node
			framePath := frame.path
			frames = frames[:frameIndex]
			if err := node.file.Close(); err != nil {
				return unsafeError(framePath, "close directory", err)
			}
			continue
		}

		directoryEntry := frame.entries[frame.next]
		frame.next++
		name := directoryEntry.Name()
		if !validChildName(name) {
			return unsafeError(frame.path, "directory contains an invalid name", nil)
		}
		childPath := name
		if frame.path != "." {
			childPath = path.Join(frame.path, name)
		}
		if hooks.beforeOpen != nil {
			if err := hooks.beforeOpen(childPath); err != nil {
				return fmt.Errorf("safefiletree: before-open hook %q: %w", childPath, err)
			}
		}

		child, err := openPlatformChild(
			frame.node,
			name,
			rootNode.device,
			options.OpenForSync,
			options.OpenForMetadataControl,
		)
		if err != nil {
			return unsafeError(childPath, "open child", err)
		}
		if err := callVisitor(
			ctx,
			options.Visit,
			childPath,
			child,
			options.OpenForSync,
			options.OpenForMetadataControl,
		); err != nil {
			verifyErr := verifyPlatformNode(child, rootNode.device)
			closeErr := child.file.Close()
			return errors.Join(
				err,
				unsafeIfError(childPath, "entry changed before visitor error", verifyErr),
				closeAsUnsafe(childPath, closeErr),
			)
		}
		if err := ctx.Err(); err != nil {
			verifyErr := verifyPlatformNode(child, rootNode.device)
			closeErr := child.file.Close()
			return errors.Join(
				err,
				unsafeIfError(childPath, "entry changed before cancellation", verifyErr),
				closeAsUnsafe(childPath, closeErr),
			)
		}
		if child.kind == KindRegularFile {
			if err := verifyPlatformNode(child, rootNode.device); err != nil {
				closeErr := child.file.Close()
				return errors.Join(
					unsafeError(childPath, "regular file changed", err),
					closeAsUnsafe(childPath, closeErr),
				)
			}
			if err := child.file.Close(); err != nil {
				return unsafeError(childPath, "close regular file", err)
			}
			continue
		}

		entries, err := readDirectoryBounded(child, limits, enumerated)
		if err != nil {
			verifyErr := verifyPlatformNode(child, rootNode.device)
			closeErr := child.file.Close()
			return errors.Join(
				unsafeError(childPath, "enumerate directory", err),
				unsafeIfError(childPath, "directory changed before enumeration error", verifyErr),
				closeAsUnsafe(childPath, closeErr),
			)
		}
		enumerated += len(entries)
		frames = append(frames, walkFrame{
			node:    child,
			path:    childPath,
			entries: entries,
		})
	}
	return nil
}

func entryForNode(
	relative string,
	node *platformNode,
	syncAllowed bool,
	metadataAllowed bool,
) (Entry, *entryAccess) {
	access := &entryAccess{
		node: node, active: true, syncAllowed: syncAllowed,
		metadataAllowed: metadataAllowed,
	}
	entry := Entry{Path: relative, Kind: node.kind, Info: node.info, access: access}
	if node.kind == KindRegularFile {
		entry.Reader = borrowedReader{access: access}
	}
	return entry, access
}

func callVisitor(
	ctx context.Context,
	visit VisitFunc,
	relative string,
	node *platformNode,
	syncAllowed bool,
	metadataAllowed bool,
) error {
	if visit == nil {
		return nil
	}
	entry, access := entryForNode(relative, node, syncAllowed, metadataAllowed)
	defer access.expire()
	if err := visit(ctx, entry); err != nil {
		return fmt.Errorf("safefiletree: visit %q: %w", relative, err)
	}
	return nil

}

func callDirectoryLeave(
	ctx context.Context,
	leave VisitFunc,
	relative string,
	node *platformNode,
	syncAllowed bool,
	metadataAllowed bool,
) error {
	if leave == nil {
		return nil
	}
	entry, access := entryForNode(relative, node, syncAllowed, metadataAllowed)
	defer access.expire()
	if err := leave(ctx, entry); err != nil {
		return fmt.Errorf("safefiletree: leave directory %q: %w", relative, err)
	}
	return nil
}

func resolveEntryLimits(options Options) (entryLimits, error) {
	if options.MaxEntries < 0 || options.MaxEntriesPerDirectory < 0 {
		return entryLimits{}, fmt.Errorf("%w: directory entry limits are invalid", ErrUnsafeTree)
	}
	limits := entryLimits{
		maxEntries:             options.MaxEntries,
		maxEntriesPerDirectory: options.MaxEntriesPerDirectory,
	}
	if limits.maxEntries == 0 {
		limits.maxEntries = DefaultMaxEntries
	}
	if limits.maxEntriesPerDirectory == 0 {
		limits.maxEntriesPerDirectory = DefaultMaxEntriesPerDirectory
	}
	return limits, nil
}

func readDirectoryBounded(
	node *platformNode,
	limits entryLimits,
	alreadyEnumerated int,
) ([]os.DirEntry, error) {
	if node.kind != KindDirectory {
		return nil, errors.New("entry is not a directory")
	}
	if alreadyEnumerated < 0 || alreadyEnumerated > limits.maxEntries {
		return nil, fmt.Errorf("%w: global limit %d", ErrEntryLimit, limits.maxEntries)
	}
	entryLimit := limits.maxEntriesPerDirectory
	if remaining := limits.maxEntries - alreadyEnumerated; entryLimit > remaining {
		entryLimit = remaining
	}
	entries := make([]os.DirEntry, 0, min(entryLimit, directoryReadChunk))
	for {
		remaining := entryLimit - len(entries)
		readCount := directoryReadChunk
		if readCount > remaining+1 {
			readCount = remaining + 1
		}
		batch, err := node.file.ReadDir(readCount)
		if len(batch) > remaining {
			return nil, fmt.Errorf("%w: directory limit %d", ErrEntryLimit, entryLimit)
		}
		entries = append(entries, batch...)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(batch) == 0 {
			return nil, io.ErrNoProgress
		}
	}
	slices.SortFunc(entries, func(left, right os.DirEntry) int {
		return strings.Compare(left.Name(), right.Name())
	})
	return entries, nil
}

func validChildName(name string) bool {
	return name != "" && name != "." && name != ".." && utf8.ValidString(name) &&
		!strings.ContainsAny(name, "/\\\x00")
}

func unsafeError(relative, action string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s %q", ErrUnsafeTree, action, relative)
	}
	if errors.Is(cause, ErrEntryLimit) {
		return fmt.Errorf("%w: %s %q: %w", ErrUnsafeTree, action, relative, cause)
	}
	return fmt.Errorf("%w: %s %q: %v", ErrUnsafeTree, action, relative, cause)
}

func closeAsUnsafe(relative string, err error) error {
	if err == nil {
		return nil
	}
	return unsafeError(relative, "close entry", err)
}

func unsafeIfError(relative, action string, err error) error {
	if err == nil {
		return nil
	}
	return unsafeError(relative, action, err)
}

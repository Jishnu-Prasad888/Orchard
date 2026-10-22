package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Jishnu-Prasad888/Orchard/internal/auth"
	"github.com/Jishnu-Prasad888/Orchard/internal/authz"
	"github.com/Jishnu-Prasad888/Orchard/internal/collections"
	"github.com/Jishnu-Prasad888/Orchard/internal/library"
	"github.com/Jishnu-Prasad888/Orchard/internal/librarydb"
	"github.com/Jishnu-Prasad888/Orchard/internal/media"
	"github.com/Jishnu-Prasad888/Orchard/internal/tags"
)

// What a share link can expose. A share is stored as a resource key; these
// are the only shapes the server ever writes, and the only ones the public
// routes know how to serve:
//
//	library     "<lib>"
//	folder      "<lib>/f:a/f:b"            (authz.FolderKey)
//	file        "<lib>/f:a/x:book.epub"     (authz.FileKey)
//	tag         "<lib>/t:<tag id>"          (authz.EntityKey "t")
//	collection  "<lib>/c:<collection id>"   (authz.EntityKey "c")
//
// The client names a target and the server builds the key, after checking
// the thing exists. It never accepts a raw key: a blank or mistyped one used
// to share the whole library, or nothing at all.
const (
	shareKindLibrary    = "library"
	shareKindFolder     = "folder"
	shareKindFile       = "file"
	shareKindTag        = "tag"
	shareKindCollection = "collection"
)

const (
	shareEntityTag        = "t"
	shareEntityCollection = "c"
)

// shareTargetRequest is what a client asks to share.
type shareTargetRequest struct {
	Kind string `json:"kind"`
	// Path is a folder's path relative to the library root (folder only).
	Path string `json:"path,omitempty"`
	// ID is the file, tag or collection id.
	ID string `json:"id,omitempty"`
}

// shareTarget is a share's target parsed back from its key.
type shareTarget struct {
	Kind string
	// Path is the folder's or file's path relative to the library root.
	Path string
	// ID is the tag or collection id.
	ID string
}

// shareTargetError is a target the client may not or cannot share, with a
// message fit to show them.
type shareTargetError struct {
	status  int
	code    string
	message string
}

func (e *shareTargetError) Error() string { return e.message }

func badTarget(message string) *shareTargetError {
	return &shareTargetError{status: http.StatusBadRequest, code: CodeBadRequest, message: message}
}

func missingTarget(message string) *shareTargetError {
	return &shareTargetError{status: http.StatusNotFound, code: CodeNotFound, message: message}
}

// resolveShareTarget checks that the target exists (and, for a collection,
// that it is the caller's own) and returns its resource key and display name.
func (s *Server) resolveShareTarget(
	ctx context.Context, lib *library.Library, u *auth.User, t *shareTargetRequest,
) (string, string, error) {
	if t == nil || t.Kind == "" {
		return "", "", badTarget("Choose what the link shares.")
	}
	if t.Kind == shareKindLibrary {
		return authz.LibraryKey(lib.ID), lib.Name, nil
	}
	if lib.Status == library.StatusOffline {
		return "", "", &shareTargetError{status: http.StatusConflict, code: CodeConflict,
			message: "Library is offline."}
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		return "", "", err
	}
	defer release()

	switch t.Kind {
	case shareKindFolder:
		path, ok := canonicalFolder(strings.Trim(t.Path, "/"))
		if !ok {
			return "", "", badTarget("Invalid folder path.")
		}
		if path == "" {
			return "", "", badTarget("Choose a folder, or share the whole library.")
		}
		if !media.NewService(media.NewFileStore(db, lib.ID), lib.Root).FolderExists(path) {
			return "", "", missingTarget("That folder does not exist.")
		}
		return authz.FolderKey(lib.ID, path), lastSegment(path), nil

	case shareKindFile:
		f, err := media.NewFileStore(db, lib.ID).GetByID(ctx, t.ID)
		if err != nil || f.Status != media.FileStatusPresent {
			return "", "", missingTarget("That file does not exist.")
		}
		return authz.FileKey(lib.ID, f.RelPath), f.Name, nil

	case shareKindTag:
		tag, err := tags.NewTagStore(db).GetByID(ctx, t.ID)
		if err != nil {
			return "", "", missingTarget("That tag does not exist.")
		}
		return authz.EntityKey(shareEntityTag, lib.ID, tag.ID), tag.Name, nil

	case shareKindCollection:
		// Collections are personal: only the owner can share one.
		c, err := collections.NewStore(db, lib.ID).GetOwned(ctx, t.ID, u.ID)
		if err != nil {
			return "", "", missingTarget("That collection does not exist.")
		}
		return authz.EntityKey(shareEntityCollection, lib.ID, c.ID), c.Name, nil
	}
	return "", "", badTarget(`kind must be one of "library", "folder", "file", "tag", "collection".`)
}

// parseShareTarget reads a share's key back into its target. A key in any
// other shape (one written by an older version from a raw key) is reported
// as a folder or library by its path segments, which is how it was always
// evaluated.
func parseShareTarget(libID, key string) shareTarget {
	if key == libID {
		return shareTarget{Kind: shareKindLibrary}
	}
	rest := strings.TrimPrefix(key, libID+"/")
	if !strings.Contains(rest, "/") {
		if id, ok := strings.CutPrefix(rest, shareEntityTag+":"); ok {
			return shareTarget{Kind: shareKindTag, ID: id}
		}
		if id, ok := strings.CutPrefix(rest, shareEntityCollection+":"); ok {
			return shareTarget{Kind: shareKindCollection, ID: id}
		}
	}
	segs := strings.Split(rest, "/")
	parts := make([]string, 0, len(segs))
	kind := shareKindFolder
	for i, seg := range segs {
		if name, ok := strings.CutPrefix(seg, "x:"); ok && i == len(segs)-1 {
			kind = shareKindFile
			parts = append(parts, name)
			continue
		}
		parts = append(parts, strings.TrimPrefix(seg, "f:"))
	}
	return shareTarget{Kind: kind, Path: strings.Join(parts, "/")}
}

// shareTargetName is a human name for a share's target, for the owner's list
// and the public page's title. Best effort: a target that has since gone
// away is named by its kind.
func (s *Server) shareTargetName(ctx context.Context, lib *library.Library, sh *authz.Share, t shareTarget) string {
	switch t.Kind {
	case shareKindLibrary:
		return lib.Name
	case shareKindFolder, shareKindFile:
		if t.Path == "" {
			return lib.Name
		}
		return lastSegment(t.Path)
	}
	db, release, err := librarydb.Acquire(lib.Root + "/.orchard")
	if err != nil {
		return t.Kind
	}
	defer release()
	switch t.Kind {
	case shareKindTag:
		if tag, err := tags.NewTagStore(db).GetByID(ctx, t.ID); err == nil {
			return tag.Name
		}
		return "Deleted tag"
	case shareKindCollection:
		if c, err := collections.NewStore(db, lib.ID).GetOwned(ctx, t.ID, sh.CreatedBy); err == nil {
			return c.Name
		}
		return "Deleted collection"
	}
	return t.Kind
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// shareCovers reports whether the share exposes f with every given
// capability. Path shares (library, folder, file) cover by key prefix, the
// same evaluation every grant uses; a tag or collection share covers the
// files in it right now, so taking a file out of either withdraws it.
func (s *Server) shareCovers(ctx context.Context, acc *shareAccess, f *media.File, caps ...authz.Capability) (bool, error) {
	if f.Status != media.FileStatusPresent || !acc.share.Capabilities.All(caps...) {
		return false, nil
	}
	switch acc.target.Kind {
	case shareKindTag, shareKindCollection:
		ids, err := s.shareMemberIDs(ctx, acc)
		if err != nil {
			return false, err
		}
		_, ok := ids[f.ID]
		return ok, nil
	}
	return coversKey(acc.share.ResourceKey, authz.FileKey(acc.libID, f.RelPath)), nil
}

// shareMemberIDs is the set of file ids in a tag or collection share.
func (s *Server) shareMemberIDs(ctx context.Context, acc *shareAccess) (map[string]struct{}, error) {
	db, release, err := librarydb.Acquire(acc.library.Root + "/.orchard")
	if err != nil {
		return nil, err
	}
	defer release()
	out := map[string]struct{}{}
	switch acc.target.Kind {
	case shareKindTag:
		ids, err := tags.NewTagStore(db).ListByTag(ctx, acc.target.ID)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = struct{}{}
		}
	case shareKindCollection:
		files, err := collections.NewStore(db, acc.libID).ListFiles(ctx, acc.target.ID, acc.share.CreatedBy)
		if errors.Is(err, collections.ErrNotFound) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			out[f.ID] = struct{}{}
		}
	}
	return out, nil
}

// shareMemberFiles lists the present files in a tag or collection share.
func (s *Server) shareMemberFiles(ctx context.Context, acc *shareAccess) ([]*media.File, error) {
	db, release, err := librarydb.Acquire(acc.library.Root + "/.orchard")
	if err != nil {
		return nil, err
	}
	defer release()
	switch acc.target.Kind {
	case shareKindCollection:
		files, err := collections.NewStore(db, acc.libID).ListFiles(ctx, acc.target.ID, acc.share.CreatedBy)
		if errors.Is(err, collections.ErrNotFound) {
			return []*media.File{}, nil
		}
		return files, err
	case shareKindTag:
		ids, err := tags.NewTagStore(db).ListByTag(ctx, acc.target.ID)
		if err != nil {
			return nil, err
		}
		store := media.NewFileStore(db, acc.libID)
		out := make([]*media.File, 0, len(ids))
		for _, id := range ids {
			f, err := store.GetByID(ctx, id)
			if err != nil || f.Status != media.FileStatusPresent {
				continue
			}
			out = append(out, f)
		}
		return out, nil
	}
	return []*media.File{}, nil
}

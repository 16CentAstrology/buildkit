// Package dfgitutil provides Dockerfile-specific utilities for git refs.
package dfgitutil

import (
	"net/url"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/pkg/errors"
)

// GitRef represents a git ref.
//
// Examples:
//   - "https://github.com/foo/bar.git#baz/qux:quux/quuz" is parsed into:
//     {Remote: "https://github.com/foo/bar.git", ShortName: "bar", Commit:"baz/qux", SubDir: "quux/quuz"}.
type GitRef struct {
	// Remote is the remote repository path.
	Remote string

	// ShortName is the directory name of the repo.
	// e.g., "bar" for "https://github.com/foo/bar.git"
	ShortName string

	// Ref is a commit hash, a tag, or branch name.
	// Ref is optional.
	Ref string

	// Checksum is a commit hash.
	Checksum string

	// SubDir is a directory path inside the repo.
	// SubDir is optional.
	SubDir string

	// IndistinguishableFromLocal is true for a ref that is indistinguishable from a local file path,
	// e.g., "github.com/foo/bar".
	//
	// Deprecated.
	// Instead, use a distinguishable form such as "https://github.com/foo/bar.git".
	//
	// The dockerfile frontend still accepts this form only for build contexts.
	IndistinguishableFromLocal bool

	// UnencryptedTCP is true for a ref that needs an unencrypted TCP connection,
	// e.g., "git://..." and "http://..." .
	//
	// Discouraged, although not deprecated.
	// Instead, consider using an encrypted TCP connection such as "git@github.com/foo/bar.git" or "https://github.com/foo/bar.git".
	UnencryptedTCP bool
}

// ParseGitRef parses a git ref.
func ParseGitRef(ref string) (*GitRef, bool, error) {
	res := &GitRef{}

	var (
		remote *gitutil.GitURL
		err    error
	)

	if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return nil, false, errors.WithStack(cerrdefs.ErrInvalidArgument)
	} else if strings.HasPrefix(ref, "github.com/") {
		res.IndistinguishableFromLocal = true // Deprecated
		u, err := url.Parse(ref)
		if err != nil {
			return nil, false, err
		}
		u.Scheme = "https"
		remote, err = gitutil.FromURL(u)
		if err != nil {
			return nil, false, err
		}
	} else {
		remote, err = gitutil.ParseURL(ref)
		if errors.Is(err, gitutil.ErrUnknownProtocol) {
			return nil, false, err
		}
		if err != nil {
			return nil, false, err
		}

		switch remote.Scheme {
		case gitutil.HTTPProtocol, gitutil.GitProtocol:
			res.UnencryptedTCP = true // Discouraged, but not deprecated
		}

		switch remote.Scheme {
		// An HTTP(S) URL is considered to be a valid git ref only when it has the ".git[...]" suffix.
		case gitutil.HTTPProtocol, gitutil.HTTPSProtocol:
			if !strings.HasSuffix(remote.Path, ".git") {
				return nil, false, errors.WithStack(cerrdefs.ErrInvalidArgument)
			}
		}
	}

	res.Remote = remote.Remote
	if res.IndistinguishableFromLocal {
		_, res.Remote, _ = strings.Cut(res.Remote, "://")
	}
	if remote.Opts != nil {
		res.Ref, res.SubDir = remote.Opts.Ref, remote.Opts.Subdir
	}

	repoSplitBySlash := strings.Split(res.Remote, "/")
	res.ShortName = strings.TrimSuffix(repoSplitBySlash[len(repoSplitBySlash)-1], ".git")

	if err := res.loadQuery(remote.Query); err != nil {
		return nil, true, err
	}

	return res, true, nil
}

func (gf *GitRef) loadQuery(query url.Values) error {
	if len(query) == 0 {
		return nil
	}
	var tag, branch string
	for k, v := range query {
		switch len(v) {
		case 0:
			return errors.Errorf("query %q has no value", k)
		case 1:
			if v[0] == "" {
				return errors.Errorf("query %q has no value", k)
			}
			// NOP
		default:
			return errors.Errorf("query %q has multiple values", k)
		}
		switch k {
		case "ref":
			if gf.Ref != "" && gf.Ref != v[0] {
				return errors.Errorf("ref conflicts: %q vs %q", gf.Ref, v[0])
			}
			gf.Ref = v[0]
		case "tag":
			tag = v[0]
		case "branch":
			branch = v[0]
		case "subdir":
			if gf.SubDir != "" && gf.SubDir != v[0] {
				return errors.Errorf("subdir conflicts: %q vs %q", gf.SubDir, v[0])
			}
			gf.SubDir = v[0]
		case "checksum", "commit":
			gf.Checksum = v[0]
		default:
			return errors.Errorf("unexpected query %q", k)
		}
	}
	if tag != "" {
		const tagPrefix = "refs/tags/"
		if !strings.HasPrefix(tag, tagPrefix) {
			tag = tagPrefix + tag
		}
		if gf.Ref != "" && gf.Ref != tag {
			return errors.Errorf("ref conflicts: %q vs %q", gf.Ref, tag)
		}
		gf.Ref = tag
	}
	if branch != "" {
		if tag != "" {
			// TODO: consider allowing this, when the tag actually exists on the branch
			return errors.New("branch conflicts with tag")
		}
		const branchPrefix = "refs/heads/"
		if !strings.HasPrefix(branch, branchPrefix) {
			branch = branchPrefix + branch
		}
		if gf.Ref != "" && gf.Ref != branch {
			return errors.Errorf("ref conflicts: %q vs %q", gf.Ref, branch)
		}
		gf.Ref = branch
	}
	if gf.Checksum != "" && gf.Ref == "" {
		gf.Ref = gf.Checksum
	}
	return nil
}

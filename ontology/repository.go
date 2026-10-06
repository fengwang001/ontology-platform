package ontology

import "fmt"

type repository struct {
	commits map[string]*commit
	lists   []map[string]struct{}
}

func newRepository() *repository {
	return &repository{
		commits: make(map[string]*commit),
		lists:   []map[string]struct{}{{}},
	}
}

func (r *repository) addCommit(input CommitInput) error {
	if input.ID == "" {
		return fmt.Errorf("%w: empty commit id", ErrInvalidArgument)
	}
	if _, exists := r.commits[input.ID]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateCommit, input.ID)
	}

	parents := make([]*commit, 0, len(input.ParentIDs))
	for _, parentID := range input.ParentIDs {
		if parentID == "" {
			return fmt.Errorf("%w: empty parent id", ErrInvalidArgument)
		}
		parent, ok := r.commits[parentID]
		if !ok {
			return fmt.Errorf("%w: %s", ErrParentNotFound, parentID)
		}
		parents = append(parents, parent)
	}

	files := make(map[string]string, len(input.Files))
	for path, content := range input.Files {
		if path == "" {
			return fmt.Errorf("%w: empty file path", ErrInvalidArgument)
		}
		files[path] = content
	}

	renames := make(map[string]string, len(input.Renames))
	for _, rename := range input.Renames {
		if rename.OldPath == "" || rename.NewPath == "" {
			return fmt.Errorf("%w: empty rename path", ErrInvalidArgument)
		}
		if _, seen := renames[rename.NewPath]; seen {
			return fmt.Errorf("%w: duplicate new path %s", ErrInvalidRename, rename.NewPath)
		}
		oldPathExists := len(parents) > 0
		for _, parent := range parents {
			if _, ok := parent.files[rename.OldPath]; !ok {
				oldPathExists = false
				continue
			}
			oldPathExists = true
			break
		}
		if !oldPathExists {
			return fmt.Errorf("%w: old path %s missing in all parents", ErrInvalidRename, rename.OldPath)
		}
		if _, stillExists := files[rename.OldPath]; stillExists {
			return fmt.Errorf("%w: old path %s still exists", ErrInvalidRename, rename.OldPath)
		}
		renames[rename.NewPath] = rename.OldPath
	}

	cm := &commit{
		id:      input.ID,
		parents: parents,
		files:   files,
		renames: renames,
	}
	if _, exists := r.commits[cm.id]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateCommit, cm.id)
	}
	r.commits[cm.id] = cm
	return nil
}

func (r *repository) addIgnoreList(ids []string) (int, error) {
	next := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return 0, fmt.Errorf("%w: empty ignored commit id", ErrInvalidArgument)
		}
		if _, ok := r.commits[id]; !ok {
			return 0, fmt.Errorf("%w: %s", ErrCommitNotFound, id)
		}
		next[id] = struct{}{}
	}
	r.lists = append(r.lists, next)
	return len(r.lists) - 1, nil
}

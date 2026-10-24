// Package projects : persistance des projets du studio de blocs (SQLite).
//
// Un projet = une page en cours de construction : marque, thème, SEO et une
// liste ordonnée de blocs. Chaque bloc garde un historique borné de versions
// (générations, retouches, éditions de code) avec un pointeur « version
// courante » : annuler/rétablir déplacent ce pointeur, sans jamais rien perdre.
//
// Choix pensés pour la suite (v1.0) :
//   - écritures par bloc plutôt qu'écrasement du document entier, et compteur
//     de révision par projet (rev) : base d'une future synchronisation temps
//     réel, et déjà utile contre deux onglets qui s'écrasent ;
//   - identifiants de blocs stables, fournis par le client ou l'orchestrateur ;
//   - migrations numérotées (PRAGMA user_version) : la migration 2 a ajouté
//     la mémoire de traduction (doc/I18N.md) sans toucher à l'existant.
package projects

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pilote pur Go (pas de CGO : le binaire reste autonome)
)

var (
	ErrNotFound = errors.New("projet introuvable")
	ErrConflict = errors.New("le projet a été modifié ailleurs — rechargez-le")
	ErrInvalid  = errors.New("données invalides")
	ErrLimit    = errors.New("limite atteinte")

	errTooManyBlocks = fmt.Errorf("%w : %d blocs maximum par projet", ErrLimit, MaxBlocksPerProject)
)

// Garde-fous : le studio est multi-comptes, la base ne doit pas pouvoir enfler.
const (
	MaxProjectsPerOwner = 200
	MaxBlocksPerProject = 60
	MaxBlockHTML        = 512 << 10 // 512 Ko par bloc
	MaxJSONBlob         = 16 << 10  // marque, thème, SEO
	MaxNameLen          = 120
	MaxPromptLen        = 4000
	KeepVersions        = 30 // versions conservées par bloc

	MaxTranslationLen     = 8192  // octets, source comme traduction
	MaxTranslationsPerOp  = 2000  // entrées par appel à SaveTranslations
	MaxTranslationsPerPrj = 20000 // lignes de traduction par projet, toutes langues
)

var (
	blockIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	blockTypeRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	projectIDRe = regexp.MustCompile(`^[a-z0-9]{8,32}$`)
	langRe      = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})?$`)
	hashRe      = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// ValidID : forme d'un identifiant de projet (défense en profondeur côté routes).
func ValidID(id string) bool { return projectIDRe.MatchString(id) }

// Origine d'une version (affichée dans l'historique du bloc).
const (
	SourceGenerate = "generate"
	SourceVariant  = "variant"
	SourceRetouch  = "retouch"
	SourceRetheme  = "retheme"
	SourceCode     = "code"
	SourceEdit     = "edit"
	SourceImport   = "import"
	SourceImage    = "image"
	sourceRestore  = "restore" // interne : copie de la version courante avant de diverger
)

var validSources = map[string]bool{
	SourceGenerate: true, SourceVariant: true, SourceRetouch: true, SourceRetheme: true,
	SourceCode: true, SourceEdit: true, SourceImport: true, SourceImage: true,
}

type Block struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	HTML      string `json:"html"`
	Prompt    string `json:"prompt"`
	VersionID int64  `json:"version_id"`
	CanUndo   bool   `json:"can_undo"`
	CanRedo   bool   `json:"can_redo"`
	UpdatedAt string `json:"updated_at"`
}

type Project struct {
	ID        string          `json:"id"`
	Owner     string          `json:"owner"`
	Name      string          `json:"name"`
	Brand     json.RawMessage `json:"brand"`
	Theme     json.RawMessage `json:"theme"`
	SEO       json.RawMessage `json:"seo"`
	Publish   json.RawMessage `json:"publish"`
	Rev       int64           `json:"rev"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	Blocks    []Block         `json:"blocks"`
}

type Summary struct {
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	Blocks    int    `json:"blocks"`
	Rev       int64  `json:"rev"`
	UpdatedAt string `json:"updated_at"`
}

type Version struct {
	ID        int64  `json:"id"`
	HTML      string `json:"html"`
	Prompt    string `json:"prompt"`
	Source    string `json:"source"`
	CreatedAt string `json:"created_at"`
	Current   bool   `json:"current"`
}

// BlockInput : un bloc tel qu'envoyé par le studio (ou un import JSON).
type BlockInput struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	HTML   string `json:"html"`
	Prompt string `json:"prompt"`
	Source string `json:"source"`
}

// ProjectInput : création (nouveau projet, import JSON, migration localStorage).
type ProjectInput struct {
	Name   string          `json:"name"`
	Brand  json.RawMessage `json:"brand"`
	Theme  json.RawMessage `json:"theme"`
	SEO    json.RawMessage `json:"seo"`
	Blocks []BlockInput    `json:"blocks"`
}

// MetaInput : mise à jour partielle (nil = inchangé). Order réordonne les
// blocs ET supprime ceux qui n'y figurent plus.
type MetaInput struct {
	Name    *string          `json:"name"`
	Brand   *json.RawMessage `json:"brand"`
	Theme   *json.RawMessage `json:"theme"`
	SEO     *json.RawMessage `json:"seo"`
	Publish *json.RawMessage `json:"publish"`
	Order   *[]string        `json:"order"`
	Rev     *int64           `json:"rev"`
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

// ---------- Ouverture & migrations ----------

var migrations = []string{
	// 1 — schéma initial
	`CREATE TABLE projects (
		id TEXT PRIMARY KEY,
		owner TEXT NOT NULL,
		name TEXT NOT NULL,
		brand TEXT NOT NULL DEFAULT '{}',
		theme TEXT NOT NULL DEFAULT '{}',
		seo TEXT NOT NULL DEFAULT '{}',
		publish TEXT NOT NULL DEFAULT '{}',
		rev INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);
	CREATE INDEX projects_owner ON projects(owner, updated_at);
	CREATE TABLE blocks (
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		id TEXT NOT NULL,
		type TEXT NOT NULL,
		position INTEGER NOT NULL,
		html TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		version_id INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (project_id, id)
	);
	CREATE TABLE block_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id TEXT NOT NULL,
		block_id TEXT NOT NULL,
		html TEXT NOT NULL,
		prompt TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT 'generate',
		created_at TEXT NOT NULL,
		FOREIGN KEY (project_id, block_id) REFERENCES blocks(project_id, id) ON DELETE CASCADE
	);
	CREATE INDEX block_versions_block ON block_versions(project_id, block_id, id);`,
	// 2 — mémoire de traduction : un texte source (hash) → sa traduction par langue
	`CREATE TABLE translations (
		project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
		lang TEXT NOT NULL,
		hash TEXT NOT NULL,
		src TEXT NOT NULL,
		text TEXT NOT NULL,
		status TEXT NOT NULL CHECK(status IN ('machine','human')),
		updated_at TEXT NOT NULL,
		PRIMARY KEY (project_id, lang, hash)
	);`,
}

// Open ouvre (ou crée) la base. WAL : lectures non bloquées par l'écriture ;
// _txlock=immediate : une transaction d'écriture prend son verrou d'entrée
// (pas d'interblocage à la promotion lecture → écriture).
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration de la base projets : %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d : %w", i+1, err)
		}
		// PRAGMA n'accepte pas de paramètre lié ; i est un entier maîtrisé.
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// ---------- Validation ----------

func (s *Store) stamp() string { return s.now().UTC().Format(time.RFC3339) }

func newID() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err) // pas d'entropie : rien de sûr à faire
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w : %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func cleanName(n string) string {
	n = strings.TrimSpace(n)
	if r := []rune(n); len(r) > MaxNameLen {
		n = string(r[:MaxNameLen])
	}
	return n
}

// cleanJSON : un objet JSON valide et borné, ou "{}".
func cleanJSON(raw json.RawMessage, what string) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	if len(raw) > MaxJSONBlob {
		return "", invalid("%s trop volumineux", what)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", invalid("%s doit être un objet JSON", what)
	}
	out, _ := json.Marshal(obj)
	return string(out), nil
}

func checkBlock(b *BlockInput) error {
	if !blockIDRe.MatchString(b.ID) {
		return invalid("identifiant de bloc « %s »", b.ID)
	}
	if !blockTypeRe.MatchString(b.Type) {
		return invalid("type de bloc « %s »", b.Type)
	}
	if len(b.HTML) > MaxBlockHTML {
		return invalid("bloc %s : HTML > %d Ko", b.ID, MaxBlockHTML>>10)
	}
	if r := []rune(b.Prompt); len(r) > MaxPromptLen {
		b.Prompt = string(r[:MaxPromptLen])
	}
	if !validSources[b.Source] {
		b.Source = SourceEdit
	}
	return nil
}

// ---------- Lecture ----------

// List : projets d'un compte, du plus récent au plus ancien (all = tous, root).
func (s *Store) List(ctx context.Context, owner string, all bool) ([]Summary, error) {
	q := `SELECT p.id, p.owner, p.name, p.rev, p.updated_at,
	             (SELECT COUNT(*) FROM blocks b WHERE b.project_id = p.id)
	      FROM projects p`
	args := []any{}
	if !all {
		q += ` WHERE p.owner = ?`
		args = append(args, owner)
	}
	q += ` ORDER BY p.updated_at DESC, p.id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	return collect(rows, err, func(r scanner) (x Summary, err error) {
		err = r.Scan(&x.ID, &x.Owner, &x.Name, &x.Rev, &x.UpdatedAt, &x.Blocks)
		return
	})
}

// collect lit toutes les lignes d'une requête (err : son erreur éventuelle).
// Jamais nil : une liste vide est [].
func collect[T any](rows *sql.Rows, err error, scan func(scanner) (T, error)) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type querier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

func getProject(ctx context.Context, q querier, id string) (Project, error) {
	var p Project
	var brand, theme, seo, pub string
	err := q.QueryRowContext(ctx, `SELECT id, owner, name, brand, theme, seo, publish, rev, created_at, updated_at
		FROM projects WHERE id = ?`, id).Scan(&p.ID, &p.Owner, &p.Name, &brand, &theme, &seo, &pub, &p.Rev, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	p.Brand, p.Theme, p.SEO, p.Publish = json.RawMessage(brand), json.RawMessage(theme), json.RawMessage(seo), json.RawMessage(pub)

	rows, err := q.QueryContext(ctx, blockSelect+` WHERE b.project_id = ? ORDER BY b.position, b.id`, id)
	p.Blocks, err = collect(rows, err, scanBlock)
	return p, err
}

// blockSelect calcule can_undo/can_redo : existe-t-il une version avant/après
// la version courante ? (L'ordre chronologique = l'ordre des id.)
const blockSelect = `SELECT b.id, b.type, b.html, b.prompt, b.version_id, b.updated_at,
	EXISTS(SELECT 1 FROM block_versions v WHERE v.project_id = b.project_id AND v.block_id = b.id AND v.id < b.version_id),
	EXISTS(SELECT 1 FROM block_versions v WHERE v.project_id = b.project_id AND v.block_id = b.id AND v.id > b.version_id AND b.version_id > 0)
	FROM blocks b`

type scanner interface{ Scan(dest ...any) error }

func scanBlock(r scanner) (Block, error) {
	var b Block
	err := r.Scan(&b.ID, &b.Type, &b.HTML, &b.Prompt, &b.VersionID, &b.UpdatedAt, &b.CanUndo, &b.CanRedo)
	return b, err
}

func getBlock(ctx context.Context, tx *sql.Tx, pid, bid string) (Block, error) {
	return scanBlock(tx.QueryRowContext(ctx, blockSelect+` WHERE b.project_id = ? AND b.id = ?`, pid, bid))
}

func (s *Store) Get(ctx context.Context, id string) (Project, error) {
	return getProject(ctx, s.db, id)
}

// Owner : propriétaire seul (contrôle d'accès des routes, sans charger les blocs).
func (s *Store) Owner(ctx context.Context, id string) (string, error) {
	var owner string
	err := s.db.QueryRowContext(ctx, `SELECT owner FROM projects WHERE id = ?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

// ---------- Écriture ----------

func (s *Store) tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// bump incrémente la révision (après contrôle optimiste si rev est fourni).
func (s *Store) bump(ctx context.Context, tx *sql.Tx, id string, rev *int64) (int64, error) {
	var cur int64
	err := tx.QueryRowContext(ctx, `SELECT rev FROM projects WHERE id = ?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if rev != nil && *rev != cur {
		return cur, ErrConflict
	}
	cur++
	_, err = tx.ExecContext(ctx, `UPDATE projects SET rev = ?, updated_at = ? WHERE id = ?`, cur, s.stamp(), id)
	return cur, err
}

func (s *Store) Create(ctx context.Context, owner string, in ProjectInput) (Project, error) {
	name := cleanName(in.Name)
	if name == "" {
		name = "Ma page"
	}
	brand, err := cleanJSON(in.Brand, "marque")
	if err != nil {
		return Project{}, err
	}
	theme, err := cleanJSON(in.Theme, "thème")
	if err != nil {
		return Project{}, err
	}
	seo, err := cleanJSON(in.SEO, "SEO")
	if err != nil {
		return Project{}, err
	}
	if len(in.Blocks) > MaxBlocksPerProject {
		return Project{}, errTooManyBlocks
	}
	seen := map[string]bool{}
	for i := range in.Blocks {
		if in.Blocks[i].ID == "" || seen[in.Blocks[i].ID] {
			in.Blocks[i].ID = "b" + newID()[:9] // import : identifiant absent ou en double
		}
		seen[in.Blocks[i].ID] = true
		if in.Blocks[i].Source == "" {
			in.Blocks[i].Source = SourceImport
		}
		if err := checkBlock(&in.Blocks[i]); err != nil {
			return Project{}, err
		}
	}

	id := newID()
	err = s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE owner = ?`, owner).Scan(&n); err != nil {
			return err
		}
		if n >= MaxProjectsPerOwner {
			return fmt.Errorf("%w : %d projets maximum par compte", ErrLimit, MaxProjectsPerOwner)
		}
		now := s.stamp()
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id, owner, name, brand, theme, seo, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?)`, id, owner, name, brand, theme, seo, now, now); err != nil {
			return err
		}
		for i, b := range in.Blocks {
			if err := s.upsertBlock(ctx, tx, id, b, i); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Project{}, err
	}
	return s.Get(ctx, id)
}

// upsertBlock écrit le bloc et, si son HTML a changé, empile une version.
// position < 0 ⇒ bloc nouveau placé en fin, bloc existant laissé à sa place.
func (s *Store) upsertBlock(ctx context.Context, tx *sql.Tx, pid string, b BlockInput, position int) error {
	now := s.stamp()
	var curHTML, curPrompt string
	var curPos int
	var curVersion int64
	err := tx.QueryRowContext(ctx, `SELECT html, prompt, position, version_id FROM blocks WHERE project_id = ? AND id = ?`, pid, b.ID).
		Scan(&curHTML, &curPrompt, &curPos, &curVersion)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if !exists {
		var n, maxPos int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(position), -1) FROM blocks WHERE project_id = ?`, pid).Scan(&n, &maxPos); err != nil {
			return err
		}
		if n >= MaxBlocksPerProject {
			return errTooManyBlocks
		}
		if position < 0 {
			position = maxPos + 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO blocks (project_id, id, type, position, html, prompt, updated_at)
			VALUES (?,?,?,?,?,?,?)`, pid, b.ID, b.Type, position, b.HTML, b.Prompt, now); err != nil {
			return err
		}
	} else {
		if position < 0 {
			position = curPos
		}
		if _, err := tx.ExecContext(ctx, `UPDATE blocks SET type = ?, position = ?, html = ?, prompt = ?, updated_at = ?
			WHERE project_id = ? AND id = ?`, b.Type, position, b.HTML, b.Prompt, now, pid, b.ID); err != nil {
			return err
		}
	}
	if b.HTML == "" || (exists && b.HTML == curHTML) {
		return nil // rien de neuf à historiser
	}
	// Nouvelle version alors que le pointeur n'est pas en bout d'historique
	// (on a annulé, puis on régénère) : on recopie d'abord la version courante
	// en fin de liste. « Annuler » ramène ainsi à ce qu'on avait sous les yeux,
	// et les versions abandonnées restent consultables — rien n'est perdu.
	if exists && curVersion > 0 {
		var ahead bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM block_versions WHERE project_id = ? AND block_id = ? AND id > ?)`,
			pid, b.ID, curVersion).Scan(&ahead); err != nil {
			return err
		}
		if ahead {
			if _, err := addVersion(ctx, tx, pid, b.ID, curHTML, curPrompt, sourceRestore, now); err != nil {
				return err
			}
		}
	}
	vid, err := addVersion(ctx, tx, pid, b.ID, b.HTML, b.Prompt, b.Source, now)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE blocks SET version_id = ? WHERE project_id = ? AND id = ?`, vid, pid, b.ID); err != nil {
		return err
	}
	// Élagage : on garde les KeepVersions plus récentes (la courante est la dernière).
	_, err = tx.ExecContext(ctx, `DELETE FROM block_versions WHERE project_id = ? AND block_id = ? AND id NOT IN
		(SELECT id FROM block_versions WHERE project_id = ? AND block_id = ? ORDER BY id DESC LIMIT ?)`,
		pid, b.ID, pid, b.ID, KeepVersions)
	return err
}

func addVersion(ctx context.Context, tx *sql.Tx, pid, bid, html, prompt, source, now string) (int64, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO block_versions (project_id, block_id, html, prompt, source, created_at)
		VALUES (?,?,?,?,?,?)`, pid, bid, html, prompt, source, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveBlock : création ou mise à jour d'un bloc. Renvoie le bloc et la
// nouvelle révision du projet.
func (s *Store) SaveBlock(ctx context.Context, pid string, in BlockInput, rev *int64) (Block, int64, error) {
	if err := checkBlock(&in); err != nil {
		return Block{}, 0, err
	}
	var out Block
	var newRev int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		if newRev, err = s.bump(ctx, tx, pid, rev); err != nil {
			return err
		}
		if err := s.upsertBlock(ctx, tx, pid, in, -1); err != nil {
			return err
		}
		out, err = getBlock(ctx, tx, pid, in.ID)
		return err
	})
	return out, newRev, err
}

// UpdateMeta : nom, marque, thème, SEO, dernier déploiement et ordre des blocs.
func (s *Store) UpdateMeta(ctx context.Context, pid string, in MetaInput) (Project, error) {
	sets, args := []string{}, []any{}
	if in.Name != nil {
		n := cleanName(*in.Name)
		if n == "" {
			return Project{}, invalid("nom de projet vide")
		}
		sets, args = append(sets, "name = ?"), append(args, n)
	}
	for _, f := range []struct {
		col, what string
		raw       *json.RawMessage
	}{{"brand", "marque", in.Brand}, {"theme", "thème", in.Theme}, {"seo", "SEO", in.SEO}, {"publish", "publication", in.Publish}} {
		if f.raw == nil {
			continue
		}
		v, err := cleanJSON(*f.raw, f.what)
		if err != nil {
			return Project{}, err
		}
		sets, args = append(sets, f.col+" = ?"), append(args, v)
	}
	if in.Order != nil {
		seen := map[string]bool{}
		for _, id := range *in.Order {
			if !blockIDRe.MatchString(id) || seen[id] {
				return Project{}, invalid("ordre des blocs")
			}
			seen[id] = true
		}
	}

	var out Project
	err := s.tx(ctx, func(tx *sql.Tx) error {
		if _, err := s.bump(ctx, tx, pid, in.Rev); err != nil {
			return err
		}
		if len(sets) > 0 {
			q := `UPDATE projects SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
			if _, err := tx.ExecContext(ctx, q, append(args, pid)...); err != nil {
				return err
			}
		}
		if in.Order != nil {
			// Deux passes : positions négatives d'abord pour repérer les absents.
			if _, err := tx.ExecContext(ctx, `UPDATE blocks SET position = -1 WHERE project_id = ?`, pid); err != nil {
				return err
			}
			for i, id := range *in.Order {
				if _, err := tx.ExecContext(ctx, `UPDATE blocks SET position = ? WHERE project_id = ? AND id = ?`, i, pid, id); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM blocks WHERE project_id = ? AND position = -1`, pid); err != nil {
				return err
			}
		}
		var err error
		out, err = getProject(ctx, tx, pid)
		return err
	})
	return out, err
}

func (s *Store) Delete(ctx context.Context, pid string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, pid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- Traductions ----------

// Translation : la traduction d'un segment source (doc/I18N.md), indexée par
// le hash du texte source — pas par le balisage, que la génération réécrit.
type Translation struct {
	Hash      string `json:"hash"`
	Src       string `json:"src"`
	Text      string `json:"text"`
	Status    string `json:"status"` // "machine" (à relire) | "human" (saisi ou validé)
	UpdatedAt string `json:"updated_at"`
}

const (
	StatusMachine = "machine"
	StatusHuman   = "human"
)

// Translations : toute la mémoire du projet, langue → hash → entrée (jamais nil).
func (s *Store) Translations(ctx context.Context, pid string) (map[string]map[string]Translation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lang, hash, src, text, status, updated_at
		FROM translations WHERE project_id = ? ORDER BY lang, hash`, pid)
	type row struct {
		lang string
		t    Translation
	}
	all, err := collect(rows, err, func(r scanner) (row, error) {
		var x row
		err := r.Scan(&x.lang, &x.t.Hash, &x.t.Src, &x.t.Text, &x.t.Status, &x.t.UpdatedAt)
		return x, err
	})
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]Translation{}
	for _, x := range all {
		if out[x.lang] == nil {
			out[x.lang] = map[string]Translation{}
		}
		out[x.lang][x.t.Hash] = x.t
	}
	return out, nil
}

// SaveTranslations enregistre un lot pour une langue, en une transaction :
// upsert, ou suppression quand Text est vide. Ne touche pas à rev : les
// traductions vivent à côté des blocs, une saisie ne doit pas mettre en
// conflit un onglet qui édite la page.
func (s *Store) SaveTranslations(ctx context.Context, pid, lang string, ts []Translation) error {
	if !langRe.MatchString(lang) {
		return invalid("langue « %s »", lang)
	}
	if len(ts) > MaxTranslationsPerOp {
		return invalid("%d traductions maximum par envoi", MaxTranslationsPerOp)
	}
	for _, t := range ts {
		switch {
		case !hashRe.MatchString(t.Hash):
			return invalid("hash de segment « %s »", t.Hash)
		case t.Text != "" && t.Status != StatusMachine && t.Status != StatusHuman:
			return invalid("statut de traduction « %s »", t.Status)
		case len(t.Src) > MaxTranslationLen || len(t.Text) > MaxTranslationLen:
			return invalid("segment %s : texte > %d octets", t.Hash, MaxTranslationLen)
		}
	}
	now := s.stamp()
	return s.tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, pid).Scan(new(int)); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		for _, t := range ts {
			var err error
			if t.Text == "" {
				_, err = tx.ExecContext(ctx, `DELETE FROM translations WHERE project_id = ? AND lang = ? AND hash = ?`, pid, lang, t.Hash)
			} else {
				_, err = tx.ExecContext(ctx, `INSERT INTO translations (project_id, lang, hash, src, text, status, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT (project_id, lang, hash) DO UPDATE SET src = excluded.src, text = excluded.text,
						status = excluded.status, updated_at = excluded.updated_at`,
					pid, lang, t.Hash, t.Src, t.Text, t.Status, now)
			}
			if err != nil {
				return err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM translations WHERE project_id = ?`, pid).Scan(&n); err != nil {
			return err
		}
		if n > MaxTranslationsPerPrj {
			return fmt.Errorf("%w : %d traductions maximum par projet", ErrLimit, MaxTranslationsPerPrj)
		}
		return nil
	})
}

// ---------- Historique ----------

func (s *Store) Versions(ctx context.Context, pid, bid string) ([]Version, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT v.id, v.html, v.prompt, v.source, v.created_at, v.id = b.version_id
		FROM block_versions v JOIN blocks b ON b.project_id = v.project_id AND b.id = v.block_id
		WHERE v.project_id = ? AND v.block_id = ? ORDER BY v.id DESC`, pid, bid)
	return collect(rows, err, func(r scanner) (v Version, err error) {
		err = r.Scan(&v.ID, &v.HTML, &v.Prompt, &v.Source, &v.CreatedAt, &v.Current)
		return
	})
}

// Checkout place le bloc sur une version existante (restaurer depuis la liste).
func (s *Store) Checkout(ctx context.Context, pid, bid string, versionID int64, rev *int64) (Block, int64, error) {
	return s.moveTo(ctx, pid, bid, rev, `v.id = ?`, versionID)
}

// Step : annuler (dir < 0) ou rétablir (dir > 0) — la version voisine de la
// courante dans l'ordre chronologique.
func (s *Store) Step(ctx context.Context, pid, bid string, dir int, rev *int64) (Block, int64, error) {
	if dir < 0 {
		return s.moveTo(ctx, pid, bid, rev, `v.id < b.version_id ORDER BY v.id DESC LIMIT 1`)
	}
	return s.moveTo(ctx, pid, bid, rev, `v.id > b.version_id ORDER BY v.id ASC LIMIT 1`)
}

func (s *Store) moveTo(ctx context.Context, pid, bid string, rev *int64, cond string, condArgs ...any) (Block, int64, error) {
	var out Block
	var newRev int64
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var vid int64
		var html, prompt string
		args := append([]any{pid, bid}, condArgs...)
		err := tx.QueryRowContext(ctx, `SELECT v.id, v.html, v.prompt FROM block_versions v
			JOIN blocks b ON b.project_id = v.project_id AND b.id = v.block_id
			WHERE v.project_id = ? AND v.block_id = ? AND `+cond, args...).Scan(&vid, &html, &prompt)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if newRev, err = s.bump(ctx, tx, pid, rev); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE blocks SET html = ?, prompt = ?, version_id = ?, updated_at = ?
			WHERE project_id = ? AND id = ?`, html, prompt, vid, s.stamp(), pid, bid); err != nil {
			return err
		}
		out, err = getBlock(ctx, tx, pid, bid)
		return err
	})
	return out, newRev, err
}

// ---------- Compteurs (page /debate) ----------

// Counts : l'état du studio en chiffres, tous comptes confondus.
type Counts struct {
	Projects     int            `json:"projects"`
	Blocks       int            `json:"blocks"`
	Versions     map[string]int `json:"versions"`     // par origine (generate, variant, retouch…)
	Translations map[string]int `json:"translations"` // par statut (machine, human)
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	out := Counts{Versions: map[string]int{}, Translations: map[string]int{}}
	for _, src := range []string{SourceGenerate, SourceVariant, SourceRetouch, SourceRetheme, SourceEdit, SourceCode, SourceImport, SourceImage} {
		out.Versions[src] = 0
	}
	out.Translations[StatusMachine], out.Translations[StatusHuman] = 0, 0
	if err := s.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM projects), (SELECT COUNT(*) FROM blocks)`).Scan(&out.Projects, &out.Blocks); err != nil {
		return out, err
	}
	for q, m := range map[string]map[string]int{
		`SELECT source, COUNT(*) FROM block_versions GROUP BY source`: out.Versions,
		`SELECT status, COUNT(*) FROM translations GROUP BY status`:   out.Translations,
	} {
		rows, err := s.db.QueryContext(ctx, q)
		if err != nil {
			return out, err
		}
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				rows.Close()
				return out, err
			}
			m[k] = n
		}
		rows.Close()
	}
	return out, nil
}

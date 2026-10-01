package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/tidyfleet/tidyfleet/server/internal/alerts"
)

const sessionTTL = 30 * 24 * time.Hour

type user struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type userCtxKey struct{}

func userFrom(r *http.Request) user { return r.Context().Value(userCtxKey{}).(user) }

func (s *Server) userAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !strings.HasPrefix(tok, "tfs_") {
			writeErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		var u user
		err := s.DB.QueryRow(r.Context(), `SELECT u.id, u.org_id, u.email, u.role FROM sessions s
			JOIN users u ON u.id = s.user_id WHERE s.token_hash = $1 AND s.expires_at > now()`, HashToken(tok)).
			Scan(&u.ID, &u.OrgID, &u.Email, &u.Role)
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, http.StatusUnauthorized, "session expired; sign in again")
			return
		}
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), userCtxKey{}, u)))
	}
}

func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return s.userAuth(func(w http.ResponseWriter, r *http.Request) {
		if userFrom(r).Role != "admin" {
			writeErr(w, http.StatusForbidden, "only admins can do this")
			return
		}
		h(w, r)
	})
}

// dummyHash keeps login timing the same whether or not the email exists.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("tidyfleet-dummy"), bcrypt.DefaultCost)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, 4<<10, true, &req) {
		return
	}
	var u user
	var hash []byte
	err := s.DB.QueryRow(r.Context(), `SELECT id, org_id, email, role, password_hash FROM users WHERE lower(email) = lower($1)`,
		strings.TrimSpace(req.Email)).Scan(&u.ID, &u.OrgID, &u.Email, &u.Role, &hash)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.internalErr(w, r, err)
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		hash = dummyHash
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(req.Password)) != nil || errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, "wrong email or password")
		return
	}
	token, th := NewToken("tfs_")
	expires := time.Now().Add(sessionTTL).UTC()
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`, th, u.ID, expires); err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": expires, "user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if _, err := s.DB.Exec(r.Context(), `DELETE FROM sessions WHERE token_hash = $1`, HashToken(bearer(r))); err != nil {
		s.internalErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r)
	var orgName string
	if err := s.DB.QueryRow(r.Context(), `SELECT name FROM orgs WHERE id = $1`, u.OrgID).Scan(&orgName); err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u, "org_name": orgName})
}

// Enrollment codes use Crockford's base32 alphabet (no I, L, O, U) so they
// are easy to read aloud and type: TF-XXXXX-XXXXX, about 50 bits.
const codeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func NewEnrollCode() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)] // 256 % 32 == 0: unbiased
	}
	return fmt.Sprintf("TF-%s-%s", b[:5], b[5:])
}

// CreateOrg creates an organization with the default alert rules and its
// first admin, and returns the enrollment code.
func CreateOrg(ctx context.Context, db *pgxpool.Pool, name, email, password string) (orgID, code string, err error) {
	name, email = strings.TrimSpace(name), strings.TrimSpace(email)
	if name == "" || !strings.Contains(email, "@") {
		return "", "", errors.New("an organization name and a valid admin email are required")
	}
	if len(password) < 10 {
		return "", "", errors.New("the admin password must be at least 10 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	code = NewEnrollCode()
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO orgs (name, enroll_code) VALUES ($1, $2) RETURNING id`, name, code).Scan(&orgID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO users (org_id, email, password_hash, role) VALUES ($1, $2, $3, 'admin')`, orgID, email, hash); err != nil {
			if strings.Contains(err.Error(), "users_email_key") {
				return fmt.Errorf("a user with email %s already exists", email)
			}
			return err
		}
		for _, r := range alerts.Defaults {
			if _, err := tx.Exec(ctx, `INSERT INTO alert_rules (org_id, name, metric, op, threshold, enabled) VALUES ($1, $2, $3, $4, $5, $6)`,
				orgID, r.Name, r.Metric, r.Op, r.Threshold, r.Enabled); err != nil {
				return err
			}
		}
		return nil
	})
	return orgID, code, err
}

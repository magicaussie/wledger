package settings

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/tuxedocurly/wledger/internal/db"
	"golang.org/x/crypto/bcrypt"
)

type Service interface {
	GetSettings(ctx context.Context) (db.Setting, error)
	UpdateSettings(ctx context.Context, params UpdateSettingsParams) error
	ListUsers(ctx context.Context) ([]db.ListUsersRow, error)
	GetUser(ctx context.Context, id int64) (db.User, error)
	CreateUser(ctx context.Context, params CreateUserParams) (db.CreateUserRow, error)
	UpdateUserPassword(ctx context.Context, id int64, password string) error
	DeleteUser(ctx context.Context, id int64) error
	ForceReset(ctx context.Context, id int64) error
}

func (s *service) UpdateUserPassword(ctx context.Context, id int64, password string) error {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	return s.store.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
		PasswordHash: string(hashedBytes),
		ID:           id,
	})
}

type service struct {
	store db.Store
}

func NewService(store db.Store) Service {
	return &service{
		store: store,
	}
}

type UpdateSettingsParams struct {
	RequireAuth          bool
	LocateTimeout        int
	EnableLocateTimeout  bool
	EnableDebugLogs      bool
	ColorLocate          string
	ColorOk              string
	ColorLow             string
	ColorCritical        string
}

type CreateUserParams struct {
	Email    string
	Role     string
	Password string
}

func (s *service) GetSettings(ctx context.Context) (db.Setting, error) {
	return s.store.GetSettings(ctx)
}

func (s *service) UpdateSettings(ctx context.Context, params UpdateSettingsParams) error {
	return s.store.ExecTx(ctx, func(q db.Querier) error {
		err := q.UpdateGeneralSettings(ctx, db.UpdateGeneralSettingsParams{
			RequireAuthForRead:   sql.NullBool{Bool: params.RequireAuth, Valid: true},
			LocateTimeoutSeconds: sql.NullInt64{Int64: int64(params.LocateTimeout), Valid: true},
			EnableLocateTimeout:  sql.NullBool{Bool: params.EnableLocateTimeout, Valid: true},
			EnableDebugLogs:      sql.NullBool{Bool: params.EnableDebugLogs, Valid: true},
		})
		if err != nil {
			return err
		}

		return q.UpdateColors(ctx, db.UpdateColorsParams{
			ColorLocate:        sql.NullString{String: params.ColorLocate, Valid: true},
			ColorStockOk:       sql.NullString{String: params.ColorOk, Valid: true},
			ColorStockLow:      sql.NullString{String: params.ColorLow, Valid: true},
			ColorStockCritical: sql.NullString{String: params.ColorCritical, Valid: true},
		})
	})
}

func (s *service) ListUsers(ctx context.Context) ([]db.ListUsersRow, error) {
	return s.store.ListUsers(ctx)
}

func (s *service) GetUser(ctx context.Context, id int64) (db.User, error) {
	return s.store.GetUser(ctx, id)
}

func (s *service) CreateUser(ctx context.Context, params CreateUserParams) (db.CreateUserRow, error) {
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(params.Password), bcrypt.DefaultCost)
	if err != nil {
		return db.CreateUserRow{}, fmt.Errorf("failed to hash password: %w", err)
	}

	return s.store.CreateUser(ctx, db.CreateUserParams{
		Email:                  params.Email,
		PasswordHash:           string(hashedBytes),
		Role:                   params.Role,
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
	})
}

func (s *service) DeleteUser(ctx context.Context, id int64) error {
	return s.store.DeleteUser(ctx, id)
}

func (s *service) ForceReset(ctx context.Context, id int64) error {
	return s.store.SetPasswordResetFlag(ctx, db.SetPasswordResetFlagParams{
		ChangePasswordRequired: sql.NullBool{Bool: true, Valid: true},
		ID:                     id,
	})
}

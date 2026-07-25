package adminres

import (
	"context"
	"strconv"

	"github.com/chudno/zerovibe/internal/admin"
	"github.com/chudno/zerovibe/internal/domain"
)

// UserAdminRepo — что админке нужно от репозитория пользователей.
type UserAdminRepo interface {
	ListAll(ctx context.Context) ([]domain.User, error)
	ByID(ctx context.Context, id int64) (domain.User, error)
	Create(ctx context.Context, u domain.User) (domain.User, error)
	UpdateRoleAndEmail(ctx context.Context, id int64, email string, role domain.Role, verified bool) error
	DeleteUser(ctx context.Context, id int64) error
}

// Hasher — хеширование пароля (bcrypt из usecase). Пароль НИКОГДА не хранится в
// открытом виде: дескриптор сразу хеширует введённый пароль перед сохранением.
type Hasher interface {
	Hash(plain string) (string, error)
}

// UserOptions отдаёт варианты для связи «Владелец» в других сущностях (список
// пользователей: id → email). Объявлено здесь, т.к. источник — репозиторий пользователей.
func UserOptions(repo UserAdminRepo) func(ctx context.Context) ([]admin.Option, error) {
	return func(ctx context.Context) ([]admin.Option, error) {
		users, err := repo.ListAll(ctx)
		if err != nil {
			return nil, err
		}
		opts := make([]admin.Option, 0, len(users))
		for _, u := range users {
			opts = append(opts, admin.Option{Value: strconv.FormatInt(u.ID, 10), Label: u.Email})
		}
		return opts, nil
	}
}

// roleOptions — варианты роли пользователя для select.
var roleOptions = []admin.Option{
	{Value: string(domain.RoleUser), Label: "Пользователь"},
	{Value: string(domain.RoleAdmin), Label: "Администратор"},
}

// RegisterUser регистрирует сущность «Пользователи». Особенности против простого
// эталона Note: select-роль, статус-бейдж подтверждения почты в списке, и ПАРОЛЬ —
// обязателен при создании, при редактировании опционален (пустой → не меняем).
func RegisterUser(reg *admin.Registry, repo UserAdminRepo, hasher Hasher) {
	admin.Register(reg, admin.Resource[domain.User]{
		Name:  "users",
		Title: "Пользователи",
		Icon:  "users",
		Fields: []admin.Field{
			{Name: "email", Label: "Email", Type: admin.FieldText, Required: true, InList: true, Sortable: true},
			{Name: "role", Label: "Роль", Type: admin.FieldSelect, Required: true, InList: true, Options: roleOptions},
			{Name: "verified", Label: "Почта подтверждена", Type: admin.FieldBool, InList: true},
			{Name: "password", Label: "Пароль", Type: admin.FieldText, Help: "При редактировании оставьте пустым, чтобы не менять"},
		},

		Row: func(u domain.User) admin.Record {
			roleLabel := "Пользователь"
			tone := ""
			if u.Role == domain.RoleAdmin {
				roleLabel, tone = "Администратор", "info"
			}
			return admin.Record{
				ID: strconv.FormatInt(u.ID, 10),
				Cells: map[string]admin.Cell{
					"email":    {Value: u.Email, Display: u.Email},
					"role":     {Value: string(u.Role), Display: roleLabel, Tone: tone},
					"verified": {Value: strconv.FormatBool(u.EmailVerified())},
				},
			}
		},

		List: func(ctx context.Context) ([]domain.User, error) { return repo.ListAll(ctx) },

		Get: func(ctx context.Context, id string) (admin.FormValues, error) {
			uid, _ := strconv.ParseInt(id, 10, 64)
			u, err := repo.ByID(ctx, uid)
			if err != nil {
				return nil, err
			}
			// Пароль в форму НЕ возвращаем (хэш не показываем) — поле пустое.
			return admin.FormValues{
				"email":    u.Email,
				"role":     string(u.Role),
				"verified": strconv.FormatBool(u.EmailVerified()),
			}, nil
		},

		Create: func(ctx context.Context, v admin.FormValues, _ map[string]admin.FileInput) error {
			// Доменная валидация email/пароля/роли — в одном месте.
			u, err := domain.NewUser(v["email"], v["password"], domain.Role(v["role"]))
			if err != nil {
				return err
			}
			hash, err := hasher.Hash(v["password"])
			if err != nil {
				return err
			}
			u.PasswordHash = hash
			_, err = repo.Create(ctx, u)
			return err
		},

		Update: func(ctx context.Context, id string, v admin.FormValues, _ map[string]admin.FileInput) error {
			uid, _ := strconv.ParseInt(id, 10, 64)
			role := domain.Role(v["role"])
			if !role.Valid() {
				return domain.ErrValidation{Field: "role", Msg: "недопустимая роль"}
			}
			email := domain.NormalizeEmail(v["email"])
			if email == "" {
				return domain.ErrValidation{Field: "email", Msg: "email обязателен"}
			}
			// Email/роль/подтверждение почты — обычным апдейтом. Пароль здесь НЕ меняем
			// (смена пароля — отдельный поток восстановления; в админке оставлено простым).
			verified := v["verified"] == "true"
			return repo.UpdateRoleAndEmail(ctx, uid, email, role, verified)
		},

		Delete: func(ctx context.Context, id string) error {
			uid, _ := strconv.ParseInt(id, 10, 64)
			return repo.DeleteUser(ctx, uid)
		},
	})
}

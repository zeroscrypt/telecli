package auth

// Статус пользователя по данным TDLib — «в сети / был(а) недавно» и т.п.
// Разбирается здесь, в auth, а не в интерфейсе: сырой JSON TDLib разбирается в
// одном месте всего проекта, и вызывающая сторона (internal/tgwall) получает
// уже классифицированное значение, а не @type-строку, смысл которой пришлось бы
// угадывать в двух местах сразу.
//
// Схема сверена с ~/.local/tdlib/include/td/telegram/td_api.h (сборка TDLib из
// master, та же, что и в остальном проекте), ID -1529460876 / -759984891 /
// 262824117 / 310385495 / -1194644996 / 164646985:
//
//	userStatusOnline    { int32 expires_; }        -1529460876
//	userStatusOffline   { int32 was_online_; }     -759984891
//	userStatusRecently  { bool by_my_privacy_settings_; } 262824117
//	userStatusLastWeek  { bool by_my_privacy_settings_; } 310385495
//	userStatusLastMonth { bool by_my_privacy_settings_; } -1194644996
//	userStatusEmpty     { }                       164646985
//
// Последние три не несут точного времени намеренно: TDLib скрывает его из-за
// настроек приватности собеседника, поэтому и показывать точнее нельзя, и не
// нужно — «был(а) недавно» это и есть их точный смысл.

// UserStatusKind — классифицированный статус пользователя: шесть вариантов
// userStatus плюс UserStatusEmpty на «данных нет».
type UserStatusKind int

const (
	// UserStatusEmpty — userStatusEmpty: статуса нет (заблокированный аккаунт,
	// скрытый профиль, ещё не загруженные данные). Показывать нечего.
	UserStatusEmpty UserStatusKind = iota
	// UserStatusOnline — userStatusOnline: сейчас в сети.
	UserStatusOnline
	// UserStatusOffline — userStatusOffline: вышел из сети, точное время выхода
	// лежит в UserStatus.WasOnline.
	UserStatusOffline
	// UserStatusRecently — userStatusRecently: был в сети недавно.
	UserStatusRecently
	// UserStatusLastWeek — userStatusLastWeek: был в сети на этой неделе.
	UserStatusLastWeek
	// UserStatusLastMonth — userStatusLastMonth: был в сети в этом месяце.
	UserStatusLastMonth
)

// UserStatus — разобранный user.status. Сравнивается и копируется целиком, то
// есть пригодна и как значение в кэше, и как содержимое поля карточки.
//
// WasOnline заполняется только для UserStatusOffline (в секундах unix, как его
// присылает TDLib) и равен нулю у остальных: у них точного времени нет вовсе,
// и выдумывать ноль секунд как «момент выхода» нельзя.
type UserStatus struct {
	Kind      UserStatusKind
	WasOnline int64
}

// ParseUserStatus разбирает объект UserStatus TDLib — как вложенное поле
// status_ объекта user, так и самостоятельное поле status_ апдейта
// updateUserStatus. Одна функция на оба места: разбирать их двумя копиями
// значило бы через первую же правку получить два разных прочтения одного и того
// же объекта.
//
// Отсутствующий или неизвестный @type читается как UserStatusEmpty, а не как
// ошибка: новый вариант статуса в будущей версии TDLib не должен превращать
// карточку в «битую», молча исчезнуть из неё тоже нельзя. «Показать столько,
// сколько знаем» здесь безопаснее, чем «не показать ничего».
func ParseUserStatus(status map[string]interface{}) UserStatus {
	if status == nil {
		return UserStatus{}
	}
	switch status["@type"] {
	case "userStatusOnline":
		return UserStatus{Kind: UserStatusOnline}
	case "userStatusOffline":
		// was_online отсутствует только в битом объекте: читаем как 0, а не
		// паникуем, тогда карточка останется живой (см. контракт парсеров файла).
		wasOnline, _ := status["was_online"].(float64)
		return UserStatus{Kind: UserStatusOffline, WasOnline: int64(wasOnline)}
	case "userStatusRecently":
		return UserStatus{Kind: UserStatusRecently}
	case "userStatusLastWeek":
		return UserStatus{Kind: UserStatusLastWeek}
	case "userStatusLastMonth":
		return UserStatus{Kind: UserStatusLastMonth}
	default:
		// userStatusEmpty и всё, чего в этой версии TDLib ещё нет.
		return UserStatus{}
	}
}

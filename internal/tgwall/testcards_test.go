package tgwall

import (
	"telecli/internal/auth"
)

// Фикстуры стены для тестов вёрстки. Раньше здесь жил зашитый в код пример
// sampleCards из макета; после подключения TDLib карточки приезжают настоящие,
// и тесты вёрстки (обрезка, ширины колонок, выделение, навигация) строятся на
// тех же данных, что приезжают в живую стену, — через ту же разметку
// wallCard. Иначе проверялось бы одно, а показывалось бы другое.

// Имя и версия для тестовых моделей стены. Оба приходят в New() снаружи
// (см. Model.appName/Model.version), поэтому проверки вёрстки нижней строки
// обязаны доставать то же самое значение, какое показывает живой запуск, а не
// константу из самого пакета: иначе тест про версию в углу проходил бы на коде,
// который её не рисует вовсе.
const (
	testAppName = "TELECLi"
	testVersion = "v9.9.9-tgwall"
)

// testCards — шесть карточек из макета, те же тексты и то же их сочетание типов:
// канал, чат, личное, канал, чат, личное. Обходятся стены всех трёх типов и
// случаи «с подписью автора» и «без подписи».
func testCards() []card {
	channel := auth.Chat{ID: 1, Title: "Новости DevOps", Kind: auth.ChatChannel}
	group := auth.Chat{ID: 2, Title: "Соседи по подъезду", Kind: auth.ChatGroup}
	personal := auth.Chat{ID: 3, Title: "Андрей", Kind: auth.ChatPrivate}
	second := auth.Chat{ID: 4, Title: "Курс валют", Kind: auth.ChatChannel}
	work := auth.Chat{ID: 5, Title: "Рабочий чат", Kind: auth.ChatGroup}
	nastya := auth.Chat{ID: 6, Title: "Настя", Kind: auth.ChatPrivate}

	return wallCards([]wallMessage{
		{Chat: channel, Message: auth.Message{Text: "Собрали разбор инцидента прошлой недели — таймлайн событий и выводы для дежурных в закреплённом посте.", Date: 1}},
		{Chat: group, Message: auth.Message{Text: "Во дворе опять перекопали, машину не поставить.", SenderName: "Марина", Date: 2}},
		{Chat: personal, Message: auth.Message{Text: "Го в субботу на футбол, есть свободный час с 10 до 12, потом уже дела — если что пиши прямо сейчас, а то мест мало и запись быстро закрывается", SenderName: "Андрей", Date: 3}},
		{Chat: second, Message: auth.Message{Text: "Доллар — 96.40 ₽, евро — 104.80 ₽ по курсу ЦБ на сегодня.", Date: 4}},
		{Chat: work, Message: auth.Message{Text: "Деплой на прод в 15:00, ревью уже смерджено — все свободны.", SenderName: "Игорь", Date: 5}},
		{Chat: nastya, Message: auth.Message{Text: "Купи хлеба по дороге, пожалуйста", SenderName: "Настя", Date: 6}},
	})
}

package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований задачи 0162.
//
// Файл существует ради одной цели: убедиться, что проверки из statusbar_test.go
// ЛОВЯТ отказ от каждого из двух требований (спиннер учитывает обе загрузки;
// статичный глиф убран и колонка индикатора держит ширину). Обычный тест
// проходит и на неправильном коде, если неправильность живёт в ветке, которую
// этот тест не задевает; здесь каждая мутация — это проверка ДРУГОГО теста.
//
// Механика (копия модуля в t.TempDir, прогон go test по содержимому, требование
// падения ИМЕННО нужного теста) — та же, что в zoom_mutation_test.go, хелперы
// переиспользуются оттуда, свои дубли не заводились.

// statusBarMutants — по несколько мутаций на каждое требование задачи.
var statusBarMutants = []mutantCase{
	{
		// Требование «спиннер учитывает обе загрузки». Откат View к одному
		// источнику — ровно то состояние, из-за которого человек сообщил о
		// проблеме: стена загружена, едет история, индикатор пуст.
		name:      "индикатор снова смотрит только на загрузку стены",
		file:      "model.go",
		from:      "renderStatusBar(m.width, m.frame, m.isLoading(), m.username, m.appName, m.version)",
		to:        "renderStatusBar(m.width, m.frame, m.loading, m.username, m.appName, m.version)",
		wantFails: "TestSpinnerAnimatesWhileZoomHistoryLoads",
	},
	{
		// Требование «спиннер учитывает обе загрузки», вторая половина: даже если
		// вызов правильный, само объединение может потерять второй источник.
		name:      "isLoading забыл про загрузку истории переписки",
		file:      "model.go",
		from:      "return m.loading || (m.zoom != nil && m.zoom.loading)",
		to:        "return m.loading",
		wantFails: "TestSpinnerAnimatesWhileZoomHistoryLoads",
	},
	{
		// Объединение должно быть ИЛИ обоих источников, а не «история важнее»:
		// иначе на старте, пока грузится стена, индикатор молчал бы.
		name:      "isLoading оставил только загрузку истории переписки",
		file:      "model.go",
		from:      "return m.loading || (m.zoom != nil && m.zoom.loading)",
		to:        "return m.zoom != nil && m.zoom.loading",
		wantFails: "TestSpinnerIsAnimatedOnlyWhileSomethingLoads",
	},
	{
		// Требование «статичный глиф убран, а не заменён другим символом».
		name:      "статичный глиф вернулся на место спиннера",
		file:      "statusbar.go",
		from:      "if !loading {\n\t\treturn \"\"\n\t}",
		to:        "if !loading {\n\t\treturn \"⋯\"\n\t}",
		wantFails: "TestSpinnerIdleDrawsNoGlyphAtAll",
	},
	{
		// Требование «ширина колонки не меняется»: без пустого фона на месте
		// индикатора название уезжает на ячейку влево при включении загрузки.
		name:      "колонка индикатора схлопнулась вместо пустого фона",
		file:      "statusbar.go",
		from:      "left := renderedFill(spinnerColumnWidth, background)",
		to:        `left := ""`,
		wantFails: "TestSpinnerIdleKeepsStatusBarWidth",
	},
	{
		// Та же ширина, но «на глаз» вместо константы: колонка нулевой ширины
		// даёт ту же картину глазом, и без проверки ширины это не поймать.
		name:      "ширина колонки индикатора объявлена нулём",
		file:      "statusbar.go",
		from:      "spinnerColumnWidth = 1",
		to:        "spinnerColumnWidth = 0",
		wantFails: "TestSpinnerIdleKeepsStatusBarWidth",
	},
}

func TestStatusBarMutationsAreCaught(t *testing.T) {
	for _, mutation := range statusBarMutants {
		t.Run(mutation.name, func(t *testing.T) {
			// Свежая копия на КАЖДУЮ мутацию: две из них правят один и тот же
			// фрагмент, и на общем дереве вторая не нашла бы свой образец уже
			// изменённым первой.
			source := t.TempDir()
			if err := copyGoModule(t, source); err != nil {
				t.Fatal(err)
			}
			if err := applyMutation(t, source, mutation); err != nil {
				t.Fatal(err)
			}
			output, err := runGoTest(t, source, mutation.wantFails)
			if err == nil {
				t.Fatalf("мутация НЕ поймана: %s прошёл на изменённом коде\n%s", mutation.wantFails, tail(output))
			}
			if !strings.Contains(string(output), "--- FAIL") {
				t.Fatalf("падение выглядит не как провал теста, а как ошибка сборки —\n"+
					"такая мутация ничего не доказывает:\n%s", tail(output))
			}
			// Падать должен ИМЕННО тот тест: если упал соседний, защита может быть
			// и не покрыта, просто сломалось что-то по соседству.
			failed := firstFailure(output)
			if !strings.Contains(failed, mutation.wantFails) {
				t.Fatalf("мутация сломала другой тест (%s), а ждали %s:\n%s",
					failed, mutation.wantFails, tail(output))
			}
			t.Logf("поймана: %s", failed)
		})
	}
}

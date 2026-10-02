package auth

import "testing"

// connectionStateUpdate — updateConnectionState в том виде, в каком его
// присылает TDLib: единственное поле state, а внутри него только @type (все пять
// классов ConnectionState — пустые структуры, сверено с td_api.h).
func connectionStateUpdate(state string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateConnectionState",
		"state": map[string]interface{}{"@type": state},
	}
}

// TestParseConnectionStateUpdateKnowsEveryState — разбираются все пять состояний,
// и наружу отдаётся РОВНО та строка @type, что прислал TDLib, без сокращений:
// текст для человека выбирает вызывающая сторона, и подменять его здесь значило
// бы продублировать таблицу состояний во втором месте.
func TestParseConnectionStateUpdateKnowsEveryState(t *testing.T) {
	states := []string{
		"connectionStateWaitingForNetwork",
		"connectionStateConnectingToProxy",
		"connectionStateConnecting",
		"connectionStateUpdating",
		"connectionStateReady",
	}
	for _, want := range states {
		got, ok := ParseConnectionStateUpdate(connectionStateUpdate(want))
		if !ok {
			t.Fatalf("состояние %q не разобрано", want)
		}
		if got != want {
			t.Fatalf("разобрано %q, хотели %q", got, want)
		}
	}
}

// TestParseConnectionStateUpdateRejectsForeignUpdate — чужой апдейт не должен
// разбираться как состояние соединения: иначе чужое событие молча сменило бы
// статус на «Нет сети».
func TestParseConnectionStateUpdateRejectsForeignUpdate(t *testing.T) {
	foreign := []map[string]interface{}{
		{"@type": "updateNewMessage"},
		{"@type": "updateChatTitle", "state": map[string]interface{}{"@type": "connectionStateReady"}},
		{},
	}
	for _, update := range foreign {
		if state, ok := ParseConnectionStateUpdate(update); ok {
			t.Fatalf("чужой апдейт %v разобран как состояние %q", update, state)
		}
	}
}

// TestParseConnectionStateUpdateRejectsBrokenState — отсутствующий или не
// объектный state, равно как и объект без @type, дают ok == false: разбирать тут
// нечего, а выдумывать состояние по умолчанию нельзя (иначе битый апдейт выглядел
// бы как «соединение в порядке»).
func TestParseConnectionStateUpdateRejectsBrokenState(t *testing.T) {
	broken := []map[string]interface{}{
		{"@type": "updateConnectionState"},
		{"@type": "updateConnectionState", "state": nil},
		{"@type": "updateConnectionState", "state": "connectionStateReady"},
		{"@type": "updateConnectionState", "state": 42},
		{"@type": "updateConnectionState", "state": map[string]interface{}{}},
		{"@type": "updateConnectionState", "state": map[string]interface{}{"type": "connectionStateReady"}},
		{"@type": "updateConnectionState", "state": map[string]interface{}{"@type": 42}},
	}
	for _, update := range broken {
		if state, ok := ParseConnectionStateUpdate(update); ok {
			t.Fatalf("битый апдейт %v разобран как состояние %q", update, state)
		}
	}
}

// TestParseConnectionStateUpdatePassesUnknownStateThrough — состояние из будущей
// версии TDLib обязано ДОЙТИ до вызывающей стороны как ok == true, а не
// превратиться в ok == false: иначе его нельзя отличить от чужого @type, и
// разбор перестанет отличать «новое в TDLib» от «чужое событие».
func TestParseConnectionStateUpdatePassesUnknownStateThrough(t *testing.T) {
	const future = "connectionStateSomethingNewInFutureTdlib"
	got, ok := ParseConnectionStateUpdate(connectionStateUpdate(future))
	if !ok {
		t.Fatalf("неизвестное состояние %q не дошло до вызывающей стороны", future)
	}
	if got != future {
		t.Fatalf("дошло %q, хотели %q без сокращений", got, future)
	}
}

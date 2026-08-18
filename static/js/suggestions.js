(function () {
  // IIFE изолирует переменные модуля от глобальной области видимости
  const input = document.getElementById("text-input");
  const dropdown = document.getElementById("suggestions-dropdown");
  if (!input || !dropdown) return;

  const DEBOUNCE_MS = 300;
  const MIN_LENGTH = 3;

  let debounceTimer = null;
  let activeController = null;

  function hideDropdown() {
    dropdown.classList.add("hidden");
    dropdown.innerHTML = "";
  }

  function renderMessage(text, className) {
    dropdown.innerHTML = "";
    const item = document.createElement("div");
    item.className = className;
    item.textContent = text;
    dropdown.appendChild(item);
    dropdown.classList.remove("hidden");
  }

  function renderSuggestions(suggestions) {
    dropdown.innerHTML = "";

    if (!suggestions || suggestions.length === 0) {
      renderMessage("Нет подсказок", "suggestion-empty");
      return;
    }

    suggestions.forEach((suggestion) => {
      const item = document.createElement("div");
      item.className = "suggestion-item";
      item.textContent = suggestion;
      item.addEventListener("click", () => {
        input.value = suggestion;
        hideDropdown();
        input.focus();
      });
      dropdown.appendChild(item);
    });

    dropdown.classList.remove("hidden");
  }

  async function getSuggestions(text) {
    // Отменяем предыдущий запрос: пользователь мог напечатать ещё один
    // символ раньше, чем пришёл ответ на предыдущий, тогда старый ответ
    // не должен перезаписать более свежие подсказки
    if (activeController) activeController.abort();
    activeController = new AbortController();

    renderMessage("Загрузка подсказок...", "suggestion-empty");

    try {
      const response = await fetch("/api/suggest", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text }),
        signal: activeController.signal,
      });

      if (!response.ok) {
        renderMessage("Подсказки временно недоступны", "suggestion-error");
        return;
      }

      const data = await response.json();
      renderSuggestions(data.suggestions);
    } catch (error) {
      // AbortError — это наша же отмена устаревшего запроса, а не сбой
      if (error.name === "AbortError") return;
      renderMessage("Не удалось получить подсказки", "suggestion-error");
    }
  }

  input.addEventListener("input", (e) => {
    clearTimeout(debounceTimer);
    const value = e.target.value.trim();

    if (value.length < MIN_LENGTH) {
      hideDropdown();
      return;
    }

    debounceTimer = setTimeout(() => getSuggestions(value), DEBOUNCE_MS);
  });

  document.addEventListener("click", (e) => {
    if (!dropdown.contains(e.target) && e.target !== input) {
      hideDropdown();
    }
  });

  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape") hideDropdown();
  });
})();

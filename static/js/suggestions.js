(function () {
    "use strict";

    const studio = window.SymbolStudio;
    if (!studio) return;
    const input = studio.input;
    const dropdown = document.getElementById("suggestions-dropdown");
    const status = document.getElementById("suggestions-status");
    let timer;
    let controller;
    let version = 0;

    function hide() {
        dropdown.hidden = true;
        dropdown.replaceChildren();
        status.textContent = "";
    }

    function cancel() {
        window.clearTimeout(timer);
        controller?.abort();
        version += 1;
        hide();
    }

    function message(text, isError = false) {
        dropdown.replaceChildren(studio.element("p", "suggestion-message" + (isError ? " is-error" : ""), text));
        dropdown.hidden = false;
        status.textContent = text;
    }

    async function suggest(text, requestedVersion) {
        const active = new AbortController();
        controller = active;
        message("Ищем продолжение твоей идеи…");
        try {
            const data = await studio.request("/api/suggest", { text }, active.signal);
            if (requestedVersion !== version) return;
            const suggestions = Array.isArray(data?.suggestions) ? data.suggestions.filter((item) => typeof item === "string" && !studio.validate(item)).slice(0, 5) : [];
            if (!suggestions.length) {
                message("Пока без подсказок. Твоя идея уже хорошее начало.");
                return;
            }
            dropdown.replaceChildren();
            suggestions.forEach((suggestion) => {
                const button = studio.element("button", "suggestion-item", suggestion);
                button.type = "button";
                button.addEventListener("click", () => {
                    studio.setText(suggestion);
                    cancel();
                });
                dropdown.appendChild(button);
            });
            dropdown.hidden = false;
            status.textContent = "Подсказок: " + suggestions.length + ". Нажми Tab или Alt и стрелку вниз, чтобы выбрать.";
        } catch (error) {
            if (error.name !== "AbortError" && requestedVersion === version) message(error.message, true);
        }
    }

    input.addEventListener("input", () => {
        // Invalidate immediately, before the debounce window starts.
        cancel();
        const text = input.value.trim();
        if (text.length < 3 || studio.validate(text)) return;
        const requestedVersion = version;
        timer = window.setTimeout(() => suggest(text, requestedVersion), 300);
    });
    input.addEventListener("studio:generating", cancel);
    input.addEventListener("keydown", (event) => {
        if (event.key === "Escape") cancel();
        if (event.altKey && event.key === "ArrowDown" && !dropdown.hidden) {
            const first = dropdown.querySelector("button");
            if (first) { event.preventDefault(); first.focus(); }
        }
    });
    dropdown.addEventListener("keydown", (event) => {
        const buttons = [...dropdown.querySelectorAll("button")];
        const index = buttons.indexOf(document.activeElement);
        if (event.key === "Escape") { cancel(); input.focus(); }
        if (index < 0 || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
        event.preventDefault();
        const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
        buttons[next].focus();
    });
    document.addEventListener("pointerdown", (event) => {
        if (event.target !== input && !dropdown.contains(event.target)) cancel();
    });
    document.addEventListener("focusin", (event) => {
        if (event.target !== input && !dropdown.contains(event.target)) cancel();
    });
})();

(function () {
    "use strict";

    const form = document.getElementById("symbol-form");
    const input = document.getElementById("text-input");
    if (!form || !input || !window.fetch || !window.AbortController) return;

    const banners = Object.freeze(["standard", "shadow", "thinkertoy"]);
    const output = document.getElementById("ascii-result");
    const generateButton = document.getElementById("generate-btn");
    const copyButton = document.getElementById("copy-result");
    const downloadButton = document.getElementById("download-result");
    const status = document.getElementById("form-status");
    const feedback = document.getElementById("result-status");
    const historyKey = "symbol-web.history.v1";
    let generationController;
    let generationVersion = 0;
    let renderedText = input.value;
    let renderedBanner = selectedBanner();
    let history = readHistory();

    function element(tag, className, text) {
        const node = document.createElement(tag);
        if (className) node.className = className;
        if (text !== undefined) node.textContent = text;
        return node;
    }

    function selectedBanner() {
        return form.querySelector('input[name="banner"]:checked')?.value || "standard";
    }

    function selectBanner(banner) {
        if (!banners.includes(banner)) return;
        const radio = document.getElementById("banner-" + banner);
        radio.checked = true;
        radio.dispatchEvent(new Event("change", { bubbles: true }));
    }

    function setText(text, banner) {
        input.value = text;
        if (banner) selectBanner(banner);
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.focus();
    }

    function setBusy(button, busy) {
        button.disabled = busy;
        button.setAttribute("aria-busy", String(busy));
    }

    function validate(text) {
        if (text === "") return "Введи текст для ASCII-арта.";
        if (text.length > 1000) return "Сократи текст до 1000 знаков.";
        if (/[^\x20-\x7E\n\r]/.test(text)) return "Используй латиницу, цифры и ASCII-символы. Переносы строк разрешены.";
        return "";
    }

    function notify(message, isError = false) {
        status.textContent = message;
        status.classList.toggle("is-error", isError);
    }

    // Each caller owns cancellation; the deadline also covers response parsing.
    async function request(url, body, signal, isForm = false) {
        const controller = new AbortController();
        let timedOut = false;
        const abort = () => controller.abort();
        if (signal?.aborted) abort();
        signal?.addEventListener("abort", abort, { once: true });
        const timer = window.setTimeout(() => {
            timedOut = true;
            controller.abort();
        }, 12000);
        try {
            const response = await fetch(url, {
                method: "POST",
                credentials: "same-origin",
                headers: {
                    "Accept": "application/json",
                    "Content-Type": isForm ? "application/x-www-form-urlencoded;charset=UTF-8" : "application/json",
                },
                body: isForm ? body.toString() : JSON.stringify(body),
                signal: controller.signal,
            });
            let data;
            try {
                data = await response.json();
            } catch (error) {
                if (controller.signal.aborted) throw error;
                throw new Error("Сервер прислал некорректный ответ. Попробуй ещё раз.");
            }
            if (!response.ok) {
                const message = typeof data?.error === "string" ? data.error : "Сервис временно недоступен. Попробуй ещё раз.";
                throw new Error(message);
            }
            return data;
        } catch (error) {
            if (signal?.aborted) throw new DOMException("Запрос отменён", "AbortError");
            if (timedOut) throw new Error("Сервис отвечает слишком долго. Попробуй ещё раз.");
            if (error instanceof TypeError) throw new Error("Не удалось связаться с сервером. Проверь подключение и попробуй ещё раз.");
            throw error;
        } finally {
            window.clearTimeout(timer);
            signal?.removeEventListener("abort", abort);
        }
    }

    function updateResultState() {
        const hasResult = !output.hidden;
        const stale = hasResult && (renderedText !== input.value || renderedBanner !== selectedBanner());
        document.getElementById("result-state").textContent = stale ? "Текст или стиль изменён" : hasResult ? "Готово" : "Ждёт твою идею";
        copyButton.disabled = !hasResult || stale;
        downloadButton.disabled = !hasResult || stale;
        if (stale) feedback.textContent = "Создай арт ещё раз, чтобы обновить результат.";
    }

    function inputChanged() {
        generationVersion += 1;
        generationController?.abort();
        setBusy(generateButton, false);
        generateButton.firstElementChild.textContent = "Создать ASCII-арт";
        document.getElementById("input-count").textContent = input.value.length + " / 1000";
        input.removeAttribute("aria-invalid");
        notify("");
        feedback.textContent = "";
        updateResultState();
    }

    function renderResult(result, text, banner) {
        output.textContent = result;
        output.hidden = false;
        document.getElementById("empty-result").hidden = true;
        document.getElementById("result-filename").textContent = banner + ".txt";
        const lines = result.replace(/\n$/, "").split("\n");
        const width = lines.reduce((max, line) => Math.max(max, line.length), 0);
        document.getElementById("result-details").textContent = width + " × " + lines.length + " / UTF-8";
        renderedText = text;
        renderedBanner = banner;
        feedback.textContent = "";
        updateResultState();
        remember(text, banner);
    }

    form.addEventListener("submit", async (event) => {
        event.preventDefault();
        const text = input.value;
        const banner = selectedBanner();
        const invalid = validate(text);
        if (invalid) {
            notify(invalid, true);
            input.setAttribute("aria-invalid", "true");
            input.focus();
            return;
        }
        generationController?.abort();
        const controller = new AbortController();
        generationController = controller;
        const version = ++generationVersion;
        setBusy(generateButton, true);
        generateButton.firstElementChild.textContent = "Создаём…";
        notify("Собираем твою идею из символов…");
        input.dispatchEvent(new Event("studio:generating"));
        try {
            const data = await request("/symbol-art", new URLSearchParams({ text, banner }), controller.signal, true);
            if (version !== generationVersion) return;
            if (typeof data?.result !== "string") throw new Error("Не удалось прочитать арт. Попробуй ещё раз.");
            renderResult(data.result, text, banner);
            notify("Готово. Твой ASCII-арт можно скопировать или скачать.");
            if (window.matchMedia("(max-width: 820px)").matches) output.scrollIntoView({ block: "center", behavior: "auto" });
        } catch (error) {
            if (error.name !== "AbortError" && version === generationVersion) notify(error.message, true);
        } finally {
            if (version === generationVersion) {
                setBusy(generateButton, false);
                generateButton.firstElementChild.textContent = "Создать ASCII-арт";
            }
        }
    });

    input.addEventListener("input", inputChanged);
    form.addEventListener("change", (event) => {
        if (event.target.name === "banner") inputChanged();
    });
    document.getElementById("clear-input").addEventListener("click", () => setText(""));
    document.querySelectorAll("[data-demo]").forEach((button) => {
        button.addEventListener("click", () => setText(button.dataset.demo));
    });

    copyButton.addEventListener("click", async () => {
        const text = output.textContent;
        try {
            if (!navigator.clipboard?.writeText) throw new Error("Clipboard unavailable");
            await navigator.clipboard.writeText(text);
            feedback.textContent = "Скопировано. Вставляй туда, где живёт твоя идея.";
        } catch (_) {
            const range = document.createRange();
            range.selectNodeContents(output);
            const selection = window.getSelection();
            selection.removeAllRanges();
            selection.addRange(range);
            output.focus();
            feedback.textContent = "Арт выделен. Нажми Ctrl+C или ⌘C, чтобы скопировать.";
        }
    });

    downloadButton.addEventListener("click", () => {
        const blob = new Blob([output.textContent], { type: "text/plain;charset=utf-8" });
        const url = URL.createObjectURL(blob);
        const link = element("a");
        link.href = url;
        link.download = "symbol-web-" + renderedBanner + ".txt";
        document.body.appendChild(link);
        link.click();
        link.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 1000);
        feedback.textContent = "Текстовый файл готов к сохранению.";
    });

    function readHistory() {
        try {
            const raw = localStorage.getItem(historyKey);
            if (!raw || raw.length > 16000) return [];
            const entries = JSON.parse(raw);
            if (!Array.isArray(entries)) return [];
            return entries.filter((entry) => entry && typeof entry.text === "string" && !validate(entry.text) && banners.includes(entry.banner)).slice(0, 6);
        } catch (_) {
            return [];
        }
    }

    function remember(text, banner) {
        history = [{ text, banner }, ...history.filter((entry) => entry.text !== text || entry.banner !== banner)].slice(0, 6);
        try {
            localStorage.setItem(historyKey, JSON.stringify(history));
        } catch (_) {
            // History remains usable in memory if storage is unavailable or full.
        }
        renderHistory();
    }

    function renderHistory() {
        const list = document.getElementById("history-list");
        list.replaceChildren();
        document.getElementById("history-panel").hidden = history.length === 0;
        history.forEach((entry) => {
            const button = element("button", "history-item");
            button.type = "button";
            button.title = "Вернуть в редактор: " + entry.text;
            button.append(element("span", "", entry.text), element("small", "", entry.banner));
            button.addEventListener("click", () => {
                setText(entry.text, entry.banner);
                notify("Идея снова в редакторе. Нажми «Создать ASCII-арт».");
            });
            list.appendChild(button);
        });
    }

    document.getElementById("clear-history").addEventListener("click", () => {
        history = [];
        try { localStorage.removeItem(historyKey); } catch (_) { /* Storage may be blocked. */ }
        renderHistory();
        notify("История очищена.");
    });

    window.SymbolStudio = Object.freeze({ input, form, banners, request, element, selectedBanner, selectBanner, setText, setBusy, validate, notify });
    ["clear-input", "demo-controls", "result-actions"].forEach((id) => { document.getElementById(id).hidden = false; });
    inputChanged();
    if (!output.hidden) renderResult(output.textContent, input.value, selectedBanner());
    else renderHistory();
})();

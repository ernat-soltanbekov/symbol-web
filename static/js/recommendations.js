(function () {
    "use strict";

    const studio = window.SymbolStudio;
    if (!studio) return;
    const button = document.getElementById("ai-recommend-btn");
    const result = document.getElementById("recommendation-result");
    const idleLabel = button.textContent;
    let controller;
    let version = 0;
    let applyingRecommendation = false;

    function clearHighlights() {
        document.querySelectorAll(".banner-label.ai-pick").forEach((label) => label.classList.remove("ai-pick"));
    }

    function reset() {
        controller?.abort();
        version += 1;
        studio.setBusy(button, false);
        button.textContent = idleLabel;
        result.hidden = true;
        result.replaceChildren();
        clearHighlights();
    }

    studio.input.addEventListener("input", reset);
    studio.form.addEventListener("change", (event) => {
        if (event.target.name === "banner" && button.disabled && !applyingRecommendation) reset();
    });
    document.getElementById("recommend-controls").hidden = false;
    button.addEventListener("click", async () => {
        reset();
        const text = studio.input.value.trim();
        const invalid = studio.validate(text);
        if (invalid) {
            studio.notify(invalid, true);
            studio.input.focus();
            return;
        }
        const requestedVersion = version;
        const active = new AbortController();
        controller = active;
        studio.setBusy(button, true);
        button.textContent = "Подбираем стиль…";
        result.hidden = false;
        result.textContent = "Анализируем длину, регистр и символы…";
        try {
            const data = await studio.request("/api/recommend-banner", { text }, active.signal);
            if (requestedVersion !== version) return;
            if (!studio.banners.includes(data?.recommended) || typeof data.reasoning !== "string") throw new Error("Не удалось подобрать стиль. Попробуй ещё раз.");
            result.replaceChildren(
                studio.element("p", "recommendation-title", "Выбор по правилам: " + data.recommended),
                studio.element("p", "recommendation-reason", data.reasoning),
            );
            if (Array.isArray(data.alternatives)) {
                const list = studio.element("div", "alternatives-list");
                data.alternatives.slice(0, 2).forEach((alternative) => {
                    if (!studio.banners.includes(alternative?.banner) || typeof alternative.reason !== "string") return;
                    const row = studio.element("div", "alternative-row");
                    row.append(studio.element("span", "alternative-name", alternative.banner), studio.element("span", "alternative-reason", alternative.reason));
                    list.appendChild(row);
                });
                if (list.childElementCount) result.appendChild(list);
            }
            applyingRecommendation = true;
            studio.selectBanner(data.recommended);
            applyingRecommendation = false;
            document.querySelector('.banner-label[data-banner="' + data.recommended + '"]').classList.add("ai-pick");
        } catch (error) {
            if (error.name !== "AbortError" && requestedVersion === version) result.replaceChildren(studio.element("p", "recommendation-error", error.message));
        } finally {
            if (requestedVersion === version) {
                studio.setBusy(button, false);
                button.textContent = idleLabel;
            }
        }
    });
})();

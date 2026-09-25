(function () {
    "use strict";

    const studio = window.SymbolStudio;
    const modal = document.getElementById("variations-modal");
    if (!studio || !modal || typeof modal.showModal !== "function") return;
    const openButton = document.getElementById("get-variations-btn");
    const closeButton = document.getElementById("variations-close");
    const list = document.getElementById("variations-list");
    let controller;
    let version = 0;

    function cancel() {
        controller?.abort();
        version += 1;
        studio.setBusy(openButton, false);
        list.removeAttribute("aria-busy");
    }

    function renderMessage(text, isError = false) {
        list.replaceChildren(studio.element("p", "variations-message" + (isError ? " is-error" : ""), text));
    }

    async function getVariations(text) {
        cancel();
        const requestedVersion = version;
        const active = new AbortController();
        controller = active;
        renderMessage("Придумываем новые направления для твоей идеи…");
        studio.setBusy(openButton, true);
        list.setAttribute("aria-busy", "true");
        if (!modal.open) modal.showModal();
        closeButton.focus();
        try {
            const data = await studio.request("/api/variations", { text }, active.signal);
            if (requestedVersion !== version || !modal.open) return;
            const variations = Array.isArray(data?.variations) ? data.variations.filter((item) => item && typeof item.text === "string" && !studio.validate(item.text) && typeof item.description === "string" && studio.banners.includes(item.suggested_banner)).slice(0, 5) : [];
            if (!variations.length) throw new Error("Новых вариантов пока нет. Попробуй ещё раз.");
            list.replaceChildren();
            variations.forEach((variation) => {
                const card = studio.element("article", "variation-card");
                const info = studio.element("div");
                info.append(
                    studio.element("h3", "variation-card-text", variation.text),
                    studio.element("p", "variation-card-desc", variation.description),
                    studio.element("span", "variation-card-banner", variation.suggested_banner),
                );
                const useButton = studio.element("button", "variation-card-use", "Использовать ↗");
                useButton.type = "button";
                useButton.setAttribute("aria-label", "Использовать вариант: " + variation.text);
                useButton.addEventListener("click", () => {
                    modal.close();
                    studio.setText(variation.text, variation.suggested_banner);
                    studio.notify("Вариант выбран. Создай арт или добавь что-то своё.");
                });
                card.append(info, useButton);
                list.appendChild(card);
            });
        } catch (error) {
            if (error.name !== "AbortError" && requestedVersion === version && modal.open) {
                renderMessage(error.message, true);
                const retry = studio.element("button", "button button-secondary retry-button", "Попробовать ещё раз");
                retry.type = "button";
                retry.addEventListener("click", () => getVariations(text));
                list.appendChild(retry);
            }
        } finally {
            if (requestedVersion === version) {
                studio.setBusy(openButton, false);
                list.removeAttribute("aria-busy");
            }
        }
    }

    openButton.hidden = false;
    openButton.addEventListener("click", () => {
        const text = studio.input.value.trim();
        const invalid = studio.validate(text);
        if (invalid) { studio.notify(invalid, true); studio.input.focus(); return; }
        getVariations(text);
    });
    closeButton.addEventListener("click", () => modal.close());
    modal.addEventListener("close", cancel);
    modal.addEventListener("cancel", cancel);
    modal.addEventListener("click", (event) => {
        if (event.target !== modal) return;
        const rect = modal.getBoundingClientRect();
        if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) modal.close();
    });
    studio.input.addEventListener("input", () => {
        cancel();
        if (modal.open) modal.close();
    });
})();

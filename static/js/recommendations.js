(function () {
  // IIFE изолирует переменные модуля от глобальной области видимости
  const btn = document.getElementById("ai-recommend-btn");
  const result = document.getElementById("recommendation-result");
  const textInput = document.getElementById("text-input");
  const bannerRadios = document.querySelectorAll('input[name="banner"]');

  // Подстраховка: если сервер отрисовал страницу без выбранного баннера
  // (например, при GET-запросе без данных), выбираем "standard" по умолчанию
  ensureDefaultBannerSelected();

  if (!btn || !result || !textInput) return;

  function ensureDefaultBannerSelected() {
    const checked = document.querySelector('input[name="banner"]:checked');
    if (checked) return;
    const fallback = document.getElementById("banner-standard");
    if (fallback) fallback.checked = true;
  }

  function clearHighlights() {
    document.querySelectorAll(".banner-label.ai-pick").forEach((label) => {
      label.classList.remove("ai-pick");
    });
  }

  function highlightBanner(banner) {
    clearHighlights();
    const label = document.querySelector(`.banner-label[data-banner="${banner}"]`);
    if (label) label.classList.add("ai-pick");
  }

  function selectBanner(banner) {
    bannerRadios.forEach((radio) => {
      radio.checked = radio.value === banner;
    });
  }

  function renderRecommendation(data) {
    result.innerHTML = "";

    const headline = document.createElement("div");
    headline.className = "recommendation-headline";

    const badge = document.createElement("span");
    badge.className = "recommendation-badge";
    badge.textContent = data.recommended;
    headline.appendChild(badge);

    result.appendChild(headline);

    const reason = document.createElement("p");
    reason.className = "recommendation-reason";
    reason.textContent = data.reasoning;
    result.appendChild(reason);

    if (Array.isArray(data.alternatives) && data.alternatives.length > 0) {
      const list = document.createElement("div");
      list.className = "alternatives-list";

      data.alternatives.forEach((alt) => {
        const row = document.createElement("div");
        row.className = "alternative-row";

        const name = document.createElement("span");
        name.className = "alternative-name";
        name.textContent = alt.banner;
        row.appendChild(name);

        const bar = document.createElement("div");
        bar.className = "score-bar";
        const fill = document.createElement("div");
        fill.className = "score-bar-fill";
        fill.style.width = `${Math.round((alt.score || 0) * 100)}%`;
        bar.appendChild(fill);
        row.appendChild(bar);

        const reasonText = document.createElement("span");
        reasonText.className = "alternative-reason";
        reasonText.textContent = alt.reason;
        row.appendChild(reasonText);

        list.appendChild(row);
      });

      result.appendChild(list);
    }

    result.classList.remove("hidden");
    // Подсвечиваем и сразу выбираем рекомендованный баннер,
    // но пользователь всё ещё может вручную выбрать другой
    highlightBanner(data.recommended);
    selectBanner(data.recommended);
  }

  function renderError(message) {
    result.innerHTML = "";
    const error = document.createElement("p");
    error.className = "recommendation-error";
    error.textContent = message;
    result.appendChild(error);
    result.classList.remove("hidden");
  }

  btn.addEventListener("click", async () => {
    const text = textInput.value.trim();
    if (!text) {
      renderError("Введите текст перед тем, как запросить рекомендацию");
      return;
    }

    btn.disabled = true;
    const originalLabel = btn.textContent;
    btn.textContent = "Анализ...";

    try {
      const response = await fetch("/api/recommend-banner", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text }),
      });

      // 503 означает, что AI-бэкенд недоступен — отдельное сообщение,
      // чтобы отличать это от прочих ошибок сервера
      if (response.status === 503) {
        renderError("Сервис рекомендаций временно недоступен");
        return;
      }
      if (!response.ok) {
        renderError("Не удалось получить рекомендацию");
        return;
      }

      const data = await response.json();
      renderRecommendation(data);
    } catch (error) {
      renderError("Не удалось связаться с сервером");
    } finally {
      btn.disabled = false;
      btn.textContent = originalLabel;
    }
  });
})();

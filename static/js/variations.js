(function () {
  // IIFE изолирует переменные модуля от глобальной области видимости
  const openBtn = document.getElementById("get-variations-btn");
  const closeBtn = document.getElementById("variations-close");
  const backdrop = document.getElementById("variations-backdrop");
  const modal = document.getElementById("variations-modal");
  const list = document.getElementById("variations-list");
  const textInput = document.getElementById("text-input");

  if (!openBtn || !modal || !list || !textInput) return;

  function openModal() {
    modal.classList.remove("hidden");
  }

  function closeModal() {
    modal.classList.add("hidden");
  }

  function selectBanner(banner) {
    document.querySelectorAll('input[name="banner"]').forEach((radio) => {
      radio.checked = radio.value === banner;
    });
  }

  function renderMessage(text, className) {
    list.innerHTML = "";
    const el = document.createElement("p");
    el.className = className;
    el.textContent = text;
    list.appendChild(el);
  }

  function renderVariations(variations) {
    list.innerHTML = "";

    if (!variations || variations.length === 0) {
      renderMessage("Вариантов не найдено", "variations-empty");
      return;
    }

    variations.forEach((variation) => {
      const card = document.createElement("div");
      card.className = "variation-card";

      const info = document.createElement("div");

      // textContent, а не innerHTML: текст приходит от LLM и не должен
      // интерпретироваться как HTML (защита от XSS)
      const text = document.createElement("div");
      text.className = "variation-card-text";
      text.textContent = variation.text;
      info.appendChild(text);

      const desc = document.createElement("div");
      desc.className = "variation-card-desc";
      desc.textContent = variation.description;
      info.appendChild(desc);

      const banner = document.createElement("div");
      banner.className = "variation-card-banner";
      banner.textContent = variation.suggested_banner;
      info.appendChild(banner);

      card.appendChild(info);

      const useBtn = document.createElement("button");
      useBtn.type = "button";
      useBtn.className = "variation-card-use";
      useBtn.textContent = "Использовать";
      useBtn.addEventListener("click", () => {
        textInput.value = variation.text;
        selectBanner(variation.suggested_banner);
        closeModal();
      });
      card.appendChild(useBtn);

      list.appendChild(card);
    });
  }

  async function getVariations(text) {
    renderMessage("Генерация идей...", "variations-empty");
    openModal();

    try {
      const response = await fetch("/api/variations", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text }),
      });

      // 503 означает, что AI-бэкенд недоступен — отдельное сообщение,
      // чтобы отличать это от прочих ошибок сервера
      if (response.status === 503) {
        renderMessage("Сервис идей временно недоступен", "variations-error");
        return;
      }
      if (!response.ok) {
        renderMessage("Не удалось получить варианты", "variations-error");
        return;
      }

      const data = await response.json();
      renderVariations(data.variations);
    } catch (error) {
      renderMessage("Не удалось связаться с сервером", "variations-error");
    }
  }

  openBtn.addEventListener("click", () => {
    const text = textInput.value.trim();
    if (!text) {
      textInput.focus();
      return;
    }
    getVariations(text);
  });

  closeBtn.addEventListener("click", closeModal);
  backdrop.addEventListener("click", closeModal);

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !modal.classList.contains("hidden")) {
      closeModal();
    }
  });
})();

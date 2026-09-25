"use strict";

const assert = require("node:assert/strict");
const { readFileSync } = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const vm = require("node:vm");

// Run the shipped scripts against a deliberately small DOM. In particular,
// innerHTML is forbidden: API strings must remain text, including HTML-like input.
class Element {
    constructor(document, tagName = "div") {
        this.document = document;
        this.tagName = tagName;
        this.children = [];
        this.listeners = new Map();
        this.attributes = new Map();
        this.hidden = false;
        this.disabled = false;
        this.value = "";
        this.className = "";
        this.open = false;
        this._text = "";
        this.classList = {
            add: (name) => { this.className = [...new Set([...this.className.split(" ").filter(Boolean), name])].join(" "); },
            remove: (name) => { this.className = this.className.split(" ").filter((item) => item !== name).join(" "); },
            contains: (name) => this.className.split(" ").includes(name),
        };
    }
    set innerHTML(_) { throw new Error("Untrusted API text must not be inserted as HTML"); }
    set textContent(value) { this._text = String(value); this.children = []; }
    get textContent() { return this._text + this.children.map((child) => child.textContent).join(""); }
    get childElementCount() { return this.children.length; }
    addEventListener(type, listener) {
        if (!this.listeners.has(type)) this.listeners.set(type, []);
        this.listeners.get(type).push(listener);
    }
    emit(type, properties = {}) {
        const event = { type, target: this, preventDefault() {}, ...properties };
        for (const listener of this.listeners.get(type) || []) listener(event);
    }
    replaceChildren(...children) { this._text = ""; this.children = [...children]; }
    append(...children) { this.children.push(...children); }
    appendChild(child) { this.append(child); return child; }
    contains(element) { return this === element || this.children.some((child) => child.contains(element)); }
    querySelectorAll(tagName) {
        return this.children.flatMap((child) => [
            ...(child.tagName === tagName ? [child] : []), ...child.querySelectorAll(tagName),
        ]);
    }
    querySelector(tagName) { return this.querySelectorAll(tagName)[0] || null; }
    setAttribute(name, value) { this.attributes.set(name, String(value)); }
    getAttribute(name) { return this.attributes.get(name) ?? null; }
    removeAttribute(name) { this.attributes.delete(name); }
    focus() {
        this.document.activeElement = this;
        this.document.emit("focusin", { target: this });
    }
    showModal() { this.open = true; }
    close() {
        if (!this.open) return;
        this.open = false;
        this.emit("close");
    }
}

function clock() {
    let now = 0;
    let nextID = 0;
    const pending = new Map();
    return {
        setTimeout(callback, delay) {
            const id = ++nextID;
            pending.set(id, { callback, due: now + delay });
            return id;
        },
        clearTimeout(id) { pending.delete(id); },
        advance(milliseconds) {
            const target = now + milliseconds;
            for (;;) {
                const next = [...pending.entries()].sort((a, b) => a[1].due - b[1].due)[0];
                if (!next || next[1].due > target) break;
                now = next[1].due;
                pending.delete(next[0]);
                next[1].callback();
            }
            now = target;
        },
    };
}

function setup(script) {
    const document = new Element(null, "document");
    document.document = document;
    const elements = new Map();
    document.getElementById = (id) => {
        if (!elements.has(id)) elements.set(id, new Element(document));
        return elements.get(id);
    };
    const banners = ["standard", "shadow", "thinkertoy"];
    const labels = new Map(banners.map((banner) => [banner, new Element(document, "label")]));
    document.querySelectorAll = () => [...labels.values()].filter((label) => label.classList.contains("ai-pick"));
    document.querySelector = (selector) => labels.get(selector.match(/data-banner="([^"]+)"/)[1]);
    const input = document.getElementById("text-input");
    const form = document.getElementById("symbol-form");
    const requests = [];
    const selections = [];
    const fills = [];
    const notices = [];
    const timers = clock();
    const studio = {
        input,
        form,
        banners,
        validate: (text) => !text.trim() || text.length > 1000 || /[^\x20-\x7E\n\r]/.test(text) ? "Invalid input" : "",
        element(tagName, className, text) {
            const element = new Element(document, tagName);
            element.className = className || "";
            if (text !== undefined) element.textContent = text;
            return element;
        },
        request(url, body, signal) {
            // Deliberately allow completion after abort. Version guards must protect
            // the UI even if the network or JSON parsing wins the cancellation race.
            return new Promise((resolve, reject) => requests.push({ url, body, signal, resolve, reject }));
        },
        setText(text, banner) {
            fills.push({ text, banner });
            input.value = text;
            if (banner) studio.selectBanner(banner);
            input.emit("input");
            input.focus();
        },
        selectBanner(banner) {
            selections.push(banner);
            form.emit("change", { target: { name: "banner", value: banner } });
        },
        setBusy(button, busy) { button.disabled = busy; button.setAttribute("aria-busy", busy); },
        notify(message, isError) { notices.push({ message, isError }); },
    };
    document.getElementById("suggestions-dropdown").hidden = true;
    document.getElementById("recommendation-result").hidden = true;
    document.getElementById("ai-recommend-btn").textContent = "Recommend";
    const window = { SymbolStudio: studio, setTimeout: timers.setTimeout, clearTimeout: timers.clearTimeout };
    const filename = path.join(__dirname, "..", "static", "js", script);
    vm.runInNewContext(readFileSync(filename, "utf8"), { window, document, AbortController }, { filename });
    return {
        document, input, form, requests, timers, selections, fills, labels, notices,
        get: document.getElementById,
        type(text) { input.value = text; input.emit("input"); },
    };
}

async function flushResponses() {
    await Promise.resolve();
    await Promise.resolve();
}

function visibleSuggestions(harness) {
    return harness.get("suggestions-dropdown").children.map((child) => child.textContent);
}

test("suggestions wait 300 ms after the last edit", () => {
    const h = setup("suggestions.js");
    h.type("Hel");
    h.timers.advance(299);
    assert.equal(h.requests.length, 0);
    h.type("Hello");
    h.timers.advance(299);
    assert.equal(h.requests.length, 0);
    h.timers.advance(1);
    assert.equal(h.requests.length, 1);
    assert.equal(h.requests[0].url, "/api/suggest");
    assert.equal(h.requests[0].body.text, "Hello");
    assert.equal(h.requests[0].signal.aborted, false);
});

test("short, blank and unsupported input never requests suggestions", () => {
    const h = setup("suggestions.js");
    for (const value of ["", "a", "ab", "  ab  ", "   ", "Привет"]) {
        h.type(value);
        h.timers.advance(500);
    }
    assert.equal(h.requests.length, 0);
    assert.equal(h.get("suggestions-dropdown").hidden, true);
});

test("editing aborts immediately and a stale response cannot reopen the dropdown", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    assert.equal(h.get("suggestions-dropdown").hidden, false);
    h.type("Welcome");
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(h.get("suggestions-dropdown").hidden, true);
    assert.equal(h.requests.length, 1, "the replacement request must still be debounced");
    h.requests[0].resolve({ suggestions: ["Old greeting"] });
    await flushResponses();
    assert.equal(h.get("suggestions-dropdown").hidden, true);
    assert.deepEqual(visibleSuggestions(h), []);
    h.timers.advance(300);
    assert.equal(h.requests.length, 2);
    assert.equal(h.requests[1].body.text, "Welcome");
});

test("deleting below three characters aborts and never schedules a replacement", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    h.type("He");
    assert.equal(h.requests[0].signal.aborted, true);
    h.requests[0].resolve({ suggestions: ["Hello again"] });
    await flushResponses();
    h.timers.advance(1000);
    assert.equal(h.requests.length, 1);
    assert.equal(h.get("suggestions-dropdown").hidden, true);
});

test("an older response or error cannot replace newer suggestions", async () => {
    for (const failOldRequest of [false, true]) {
        const h = setup("suggestions.js");
        h.type("Hello");
        h.timers.advance(300);
        h.type("Welcome");
        h.timers.advance(300);
        h.requests[1].resolve({ suggestions: ["Welcome Team!"] });
        await flushResponses();
        if (failOldRequest) h.requests[0].reject(new Error("Old network error"));
        else h.requests[0].resolve({ suggestions: ["Old greeting"] });
        await flushResponses();
        assert.deepEqual(visibleSuggestions(h), ["Welcome Team!"]);
        assert.equal(h.get("suggestions-dropdown").hidden, false);
    }
});

test("clicking a suggestion fills literal text and cancels follow-up work", async () => {
    const h = setup("suggestions.js");
    const text = '<img src=x onerror="alert(1)">';
    h.type("Hello");
    h.timers.advance(300);
    h.requests[0].resolve({ suggestions: [null, 42, "Привет", text] });
    await flushResponses();
    const dropdown = h.get("suggestions-dropdown");
    assert.equal(dropdown.children.length, 1);
    const button = dropdown.children[0];
    assert.equal(button.tagName, "button");
    assert.equal(button.type, "button");
    assert.equal(button.textContent, text);
    assert.equal(button.childElementCount, 0, "HTML-like input must remain plain text");
    button.emit("click");
    assert.equal(h.input.value, text);
    assert.equal(h.fills[0].text, text);
    assert.equal(dropdown.hidden, true);
    assert.equal(h.get("suggestions-status").textContent, "");
    h.timers.advance(1000);
    assert.equal(h.requests.length, 1, "selection must not leave a queued suggestion request");
});

test("Escape and generation cancel pending suggestions", async () => {
    for (const event of ["escape", "generate"]) {
        const h = setup("suggestions.js");
        h.type("Hello");
        h.timers.advance(300);
        if (event === "escape") h.input.emit("keydown", { key: "Escape" });
        else h.input.emit("studio:generating");
        assert.equal(h.requests[0].signal.aborted, true);
        h.requests[0].resolve({ suggestions: ["Too late"] });
        await flushResponses();
        assert.equal(h.get("suggestions-dropdown").hidden, true);
    }
});

test("current suggestion failures are readable and clear on edit", async () => {
    const h = setup("suggestions.js");
    h.type("Hello");
    h.timers.advance(300);
    h.requests[0].reject(new Error("Service unavailable"));
    await flushResponses();
    assert.equal(h.get("suggestions-dropdown").textContent, "Service unavailable");
    assert.equal(h.get("suggestions-status").textContent, "Service unavailable");
    h.type("New input");
    assert.equal(h.get("suggestions-dropdown").hidden, true);
    assert.equal(h.get("suggestions-status").textContent, "");
});

test("stale recommendations cannot select or highlight a banner", async () => {
    const h = setup("recommendations.js");
    const button = h.get("ai-recommend-btn");
    h.type("WELCOME");
    button.emit("click");
    assert.equal(h.requests[0].url, "/api/recommend-banner");
    assert.equal(button.disabled, true);
    h.type("A new message");
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(button.disabled, false);
    h.requests[0].resolve({ recommended: "shadow", reasoning: "Old recommendation" });
    await flushResponses();
    assert.equal(h.get("recommendation-result").hidden, true);
    assert.deepEqual(h.selections, []);
    assert.equal(h.labels.get("shadow").classList.contains("ai-pick"), false);
    button.emit("click");
    h.requests[1].resolve({ recommended: "standard", reasoning: "Readable for longer text", alternatives: [] });
    await flushResponses();
    assert.deepEqual(h.selections, ["standard"]);
    assert.equal(h.labels.get("standard").classList.contains("ai-pick"), true);
    assert.equal(button.disabled, false);
    h.type("Next message");
    assert.equal(h.labels.get("standard").classList.contains("ai-pick"), false);
});

test("manual banner selection cancels an in-flight recommendation", async () => {
    const h = setup("recommendations.js");
    h.type("WELCOME");
    h.get("ai-recommend-btn").emit("click");
    h.form.emit("change", { target: { name: "banner", value: "thinkertoy" } });
    assert.equal(h.requests[0].signal.aborted, true);
    assert.equal(h.get("ai-recommend-btn").disabled, false);
    h.requests[0].resolve({ recommended: "shadow", reasoning: "Old recommendation" });
    await flushResponses();
    assert.deepEqual(h.selections, []);
    assert.equal(h.get("recommendation-result").hidden, true);
});

test("closing or editing variations cancels requests and ignores late results", async () => {
    for (const action of ["close", "edit"]) {
        const h = setup("variations.js");
        h.type("Hello");
        h.get("get-variations-btn").emit("click");
        assert.equal(h.requests[0].url, "/api/variations");
        assert.equal(h.get("variations-modal").open, true);
        if (action === "close") h.get("variations-close").emit("click");
        else h.type("Welcome");
        assert.equal(h.requests[0].signal.aborted, true);
        assert.equal(h.get("variations-modal").open, false);
        assert.equal(h.get("get-variations-btn").disabled, false);
        assert.equal(h.get("variations-list").getAttribute("aria-busy"), null);
        h.requests[0].resolve({ variations: [{ text: "OLD!", description: "Old idea", suggested_banner: "shadow" }] });
        await flushResponses();
        assert.equal(h.get("variations-modal").open, false);
        assert.equal(h.get("variations-list").querySelectorAll("button").length, 0);
        assert.deepEqual(h.fills, []);
    }
});

test("choosing a variation restores its text and banner safely", async () => {
    const h = setup("variations.js");
    h.type("Hello");
    h.get("get-variations-btn").emit("click");
    const text = "<Hello>";
    h.requests[0].resolve({ variations: [{ text, description: "<b>A greeting</b>", suggested_banner: "shadow" }] });
    await flushResponses();
    const list = h.get("variations-list");
    assert.equal(list.querySelector("h3").textContent, text);
    assert.equal(list.querySelector("p").textContent, "<b>A greeting</b>");
    list.querySelector("button").emit("click");
    assert.equal(h.input.value, text);
    assert.deepEqual(h.selections, ["shadow"]);
    assert.equal(h.get("variations-modal").open, false);
});

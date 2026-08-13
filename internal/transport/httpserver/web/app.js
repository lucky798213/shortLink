(function () {
  "use strict";

  var createForm = document.getElementById("create-form");
  var originInput = document.getElementById("origin-url");
  var customExpiryWrap = document.getElementById("custom-expiry-wrap");
  var customExpiryInput = document.getElementById("custom-expiry");
  var createButton = document.getElementById("create-button");
  var createStatus = document.getElementById("create-status");
  var createResult = document.getElementById("create-result");
  var resultCode = document.getElementById("result-code");
  var resultURL = document.getElementById("result-url");
  var resultOrigin = document.getElementById("result-origin");
  var resultExpiry = document.getElementById("result-expiry");
  var openCreatedLink = document.getElementById("open-created-link");
  var copyCreatedLink = document.getElementById("copy-created-link");

  var manageForm = document.getElementById("manage-form");
  var lookupInput = document.getElementById("lookup-code");
  var lookupButton = document.getElementById("lookup-button");
  var manageStatus = document.getElementById("manage-status");
  var manageResult = document.getElementById("manage-result");
  var detailStatus = document.getElementById("detail-status");
  var detailShortURL = document.getElementById("detail-short-url");
  var detailOrigin = document.getElementById("detail-origin");
  var detailCreated = document.getElementById("detail-created");
  var detailExpiry = document.getElementById("detail-expiry");
  var copyManagedLink = document.getElementById("copy-managed-link");
  var deleteLinkButton = document.getElementById("delete-link");
  var statsStatus = document.getElementById("stats-status");
  var statPV = document.getElementById("stat-pv");
  var statUV = document.getElementById("stat-uv");
  var statLastVisit = document.getElementById("stat-last-visit");
  var refererList = document.getElementById("referer-list");
  var agentList = document.getElementById("agent-list");

  var deleteDialog = document.getElementById("delete-dialog");
  var dialogCode = document.getElementById("dialog-code");
  var confirmDeleteButton = document.getElementById("confirm-delete");
  var cancelDeleteButton = document.getElementById("cancel-delete");
  var currentLink = null;

  function setStatus(element, message, tone) {
    element.textContent = message || "";
    if (tone) {
      element.dataset.tone = tone;
    } else {
      delete element.dataset.tone;
    }
  }

  function setLoading(button, loading, loadingLabel, defaultLabel) {
    button.disabled = loading;
    button.setAttribute("aria-busy", String(loading));
    if (button === createButton) {
      button.firstElementChild.textContent = loading ? loadingLabel : defaultLabel;
    } else {
      button.textContent = loading ? loadingLabel : defaultLabel;
    }
  }

  function apiErrorMessage(error) {
    if (error && error.name === "AbortError") {
      return "请求超时，请稍后再试。";
    }
    if (!error || typeof error.status !== "number") {
      return "暂时无法连接服务，请检查网络后重试。";
    }
    if (error.status === 400) return "提交内容不正确，请检查后重试。";
    if (error.status === 401) return "API 鉴权未通过，当前页面无法执行此操作。";
    if (error.status === 404) return "没有找到这条短链接。";
    if (error.status === 410) return "这条短链接已经过期。";
    if (error.status === 429) return "操作太频繁了，请稍等片刻。";
    if (error.status === 502 || error.status === 503 || error.status === 504) return "服务暂时不可用，请稍后重试。";
    return error.message || "操作失败，请稍后重试。";
  }

  function fetchJSON(url, options) {
    var controller = new AbortController();
    var timeout = window.setTimeout(function () { controller.abort(); }, 10000);
    var requestOptions = Object.assign({}, options || {}, {
      signal: controller.signal,
      headers: Object.assign({ "Accept": "application/json" }, (options && options.headers) || {})
    });

    return fetch(url, requestOptions).then(function (response) {
      return response.text().then(function (text) {
        var data = null;
        if (text) {
          try { data = JSON.parse(text); } catch (_) { data = null; }
        }
        if (!response.ok) {
          var error = new Error(data && data.message ? data.message : "请求失败");
          error.status = response.status;
          error.code = data && data.code;
          throw error;
        }
        return data;
      });
    }).finally(function () {
      window.clearTimeout(timeout);
    });
  }

  function parseOriginURL(rawValue) {
    var value = rawValue.trim();
    var parsed;
    try {
      parsed = new URL(value);
    } catch (_) {
      throw new Error("请输入完整的 http:// 或 https:// 链接。");
    }
    if ((parsed.protocol !== "http:" && parsed.protocol !== "https:") || !parsed.hostname) {
      throw new Error("请输入完整的 http:// 或 https:// 链接。");
    }
    return value;
  }

  function selectedExpiration() {
    var selected = createForm.querySelector('input[name="expiration"]:checked');
    return selected ? selected.value : "never";
  }

  function expirationTimestamp() {
    var selection = selectedExpiration();
    if (selection === "never") return 0;
    if (selection === "custom") {
      if (!customExpiryInput.value) throw new Error("请选择具体的到期时间。");
      var customTime = new Date(customExpiryInput.value).getTime();
      if (!Number.isFinite(customTime) || customTime <= Date.now()) {
        throw new Error("到期时间必须晚于现在。");
      }
      return Math.floor(customTime / 1000);
    }
    return Math.floor((Date.now() + Number(selection) * 24 * 60 * 60 * 1000) / 1000);
  }

  function formatTimestamp(value, neverText) {
    var timestamp = Number(value);
    if (!timestamp) return neverText;
    var date = new Date(timestamp * 1000);
    if (Number.isNaN(date.getTime())) return "—";
    return new Intl.DateTimeFormat("zh-CN", {
      year: "numeric", month: "2-digit", day: "2-digit",
      hour: "2-digit", minute: "2-digit", hour12: false
    }).format(date);
  }

  function renderCreatedLink(data) {
    resultCode.textContent = data.short_code;
    resultURL.textContent = data.short_url;
    resultURL.href = data.short_url;
    openCreatedLink.href = data.short_url;
    resultOrigin.textContent = data.origin_url;
    resultOrigin.title = data.origin_url;
    resultExpiry.textContent = formatTimestamp(data.expire_at, "永久有效");
    createResult.hidden = false;
  }

  function copyText(value) {
    if (navigator.clipboard && window.isSecureContext) {
      return navigator.clipboard.writeText(value);
    }
    return new Promise(function (resolve, reject) {
      var input = document.createElement("textarea");
      input.value = value;
      input.setAttribute("readonly", "");
      input.style.position = "fixed";
      input.style.opacity = "0";
      document.body.appendChild(input);
      input.select();
      try {
        if (!document.execCommand("copy")) throw new Error("复制失败");
        resolve();
      } catch (error) {
        reject(error);
      } finally {
        input.remove();
      }
    });
  }

  function copyWithFeedback(value, button, statusElement) {
    var originalLabel = button.textContent;
    copyText(value).then(function () {
      button.textContent = "已复制";
      setStatus(statusElement, "短链接已复制到剪贴板。", "success");
      window.setTimeout(function () { button.textContent = originalLabel; }, 1800);
    }).catch(function () {
      setStatus(statusElement, "自动复制失败，请长按或选中链接复制。", "error");
    });
  }

  createForm.addEventListener("change", function (event) {
    if (event.target.name !== "expiration") return;
    var showCustom = selectedExpiration() === "custom";
    customExpiryWrap.hidden = !showCustom;
    customExpiryInput.required = showCustom;
    if (showCustom) {
      var minimum = new Date(Date.now() + 60 * 1000);
      minimum.setMinutes(minimum.getMinutes() - minimum.getTimezoneOffset());
      customExpiryInput.min = minimum.toISOString().slice(0, 16);
      customExpiryInput.focus();
    }
  });

  createForm.addEventListener("submit", function (event) {
    event.preventDefault();
    setStatus(createStatus, "");
    var originURL;
    var expireAt;
    try {
      originURL = parseOriginURL(originInput.value);
      expireAt = expirationTimestamp();
    } catch (error) {
      setStatus(createStatus, error.message, "error");
      return;
    }

    setLoading(createButton, true, "正在生成…", "生成短链接");
    fetchJSON("/api/short-links", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ origin_url: originURL, expire_at: expireAt })
    }).then(function (data) {
      renderCreatedLink(data);
      setStatus(createStatus, "生成成功，可以复制或直接打开。", "success");
      var reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      createResult.scrollIntoView({ behavior: reduceMotion ? "auto" : "smooth", block: "nearest" });
    }).catch(function (error) {
      setStatus(createStatus, apiErrorMessage(error), "error");
    }).finally(function () {
      setLoading(createButton, false, "正在生成…", "生成短链接");
    });
  });

  copyCreatedLink.addEventListener("click", function () {
    copyWithFeedback(resultURL.href, copyCreatedLink, createStatus);
  });

  function parseShortCode(rawValue) {
    var value = rawValue.trim();
    if (!value) throw new Error("请输入六位短码或完整短链接。");
    if (/^https?:\/\//i.test(value)) {
      try {
        var parsed = new URL(value);
        var segments = parsed.pathname.split("/").filter(Boolean);
        value = segments.length ? segments[segments.length - 1] : "";
      } catch (_) {
        throw new Error("短链接格式不正确。");
      }
    }
    if (!/^[0-9A-Za-z]{6}$/.test(value)) {
      throw new Error("短码应为六位数字或英文字母。");
    }
    return value;
  }

  function statusLabel(status) {
    if (status === "expired") return "已过期";
    if (status === "not_found") return "不存在";
    return "有效";
  }

  function renderLinkDetail(data) {
    currentLink = data;
    detailStatus.textContent = statusLabel(data.status);
    detailStatus.dataset.status = data.status || "active";
    detailShortURL.textContent = data.short_url;
    detailShortURL.href = data.short_url;
    detailOrigin.textContent = data.origin_url;
    detailOrigin.href = data.origin_url;
    detailCreated.textContent = formatTimestamp(data.created_at, "—");
    detailExpiry.textContent = formatTimestamp(data.expire_at, "永久有效");
    manageResult.hidden = false;
  }

  function renderRanking(list, items, emptyText) {
    list.replaceChildren();
    if (!Array.isArray(items) || items.length === 0) {
      var empty = document.createElement("li");
      empty.className = "empty-rank";
      empty.textContent = emptyText;
      list.appendChild(empty);
      return;
    }
    items.forEach(function (item) {
      var row = document.createElement("li");
      var label = document.createElement("span");
      var count = document.createElement("span");
      label.className = "rank-label";
      label.textContent = item.value || "直接访问";
      label.title = item.value || "直接访问";
      count.className = "rank-count";
      count.textContent = String(item.count || 0);
      row.append(label, count);
      list.appendChild(row);
    });
  }

  function resetStats() {
    statPV.textContent = "—";
    statUV.textContent = "—";
    statLastVisit.textContent = "暂无记录";
    refererList.replaceChildren();
    agentList.replaceChildren();
  }

  function renderStats(data) {
    statPV.textContent = Number(data.pv || 0).toLocaleString("zh-CN");
    statUV.textContent = Number(data.uv || 0).toLocaleString("zh-CN");
    statLastVisit.textContent = formatTimestamp(data.last_visited_at, "暂无记录");
    renderRanking(refererList, data.top_referers, "暂无来源记录");
    renderRanking(agentList, data.top_user_agents, "暂无设备记录");
  }

  function loadStats(code) {
    setStatus(statsStatus, "统计读取中…");
    resetStats();
    return fetchJSON("/api/short-links/" + encodeURIComponent(code) + "/stats").then(function (data) {
      renderStats(data);
      setStatus(statsStatus, "已更新", "success");
    }).catch(function (error) {
      setStatus(statsStatus, "统计暂不可用 · " + apiErrorMessage(error), "error");
      renderRanking(refererList, [], "暂无可用数据");
      renderRanking(agentList, [], "暂无可用数据");
    });
  }

  manageForm.addEventListener("submit", function (event) {
    event.preventDefault();
    setStatus(manageStatus, "");
    var code;
    try {
      code = parseShortCode(lookupInput.value);
    } catch (error) {
      manageResult.hidden = true;
      currentLink = null;
      setStatus(manageStatus, error.message, "error");
      return;
    }

    setLoading(lookupButton, true, "查询中…", "查询短链");
    fetchJSON("/api/short-links/" + encodeURIComponent(code)).then(function (data) {
      renderLinkDetail(data);
      setStatus(manageStatus, "已找到短链详情。", "success");
      return loadStats(code);
    }).catch(function (error) {
      manageResult.hidden = true;
      currentLink = null;
      setStatus(manageStatus, apiErrorMessage(error), "error");
    }).finally(function () {
      setLoading(lookupButton, false, "查询中…", "查询短链");
    });
  });

  copyManagedLink.addEventListener("click", function () {
    if (currentLink) copyWithFeedback(currentLink.short_url, copyManagedLink, manageStatus);
  });

  deleteLinkButton.addEventListener("click", function () {
    if (!currentLink) return;
    dialogCode.textContent = currentLink.short_code + " · " + currentLink.short_url;
    if (typeof deleteDialog.showModal === "function") {
      deleteDialog.showModal();
      return;
    }
    if (window.confirm("确认删除短链 " + currentLink.short_code + "？删除后无法恢复。")) {
      deleteCurrentLink();
    }
  });

  function deleteCurrentLink() {
    if (!currentLink) return;
    var code = currentLink.short_code;
    setLoading(confirmDeleteButton, true, "删除中…", "确认删除");
    fetchJSON("/api/short-links/" + encodeURIComponent(code), { method: "DELETE" }).then(function () {
      if (deleteDialog.open) deleteDialog.close();
      manageResult.hidden = true;
      currentLink = null;
      lookupInput.value = "";
      setStatus(manageStatus, "短链 " + code + " 已删除，之后将无法访问。", "success");
      lookupInput.focus();
    }).catch(function (error) {
      if (deleteDialog.open) deleteDialog.close();
      setStatus(manageStatus, apiErrorMessage(error), "error");
    }).finally(function () {
      setLoading(confirmDeleteButton, false, "删除中…", "确认删除");
    });
  }

  confirmDeleteButton.addEventListener("click", function (event) {
    event.preventDefault();
    deleteCurrentLink();
  });

  cancelDeleteButton.addEventListener("click", function () {
    if (deleteDialog.open) deleteDialog.close();
  });
})();

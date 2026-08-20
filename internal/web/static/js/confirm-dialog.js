(function () {
  'use strict';

  var dialog = document.querySelector('[data-confirm-dialog]');
  if (!dialog) return;

  var title = dialog.querySelector('[data-confirm-title]');
  var description = dialog.querySelector('[data-confirm-description]');
  var cancelButton = dialog.querySelector('[data-confirm-cancel]');
  var acceptButton = dialog.querySelector('[data-confirm-accept]');
  var pendingForm = null;
  var pendingSubmitter = null;
  var priorFocus = null;

  function closeDialog() {
    if (dialog.open) dialog.close();
  }

  function clearDialogState() {
    document.body.classList.remove('confirm-open');
    pendingForm = null;
    pendingSubmitter = null;
    if (priorFocus && typeof priorFocus.focus === 'function') priorFocus.focus();
    priorFocus = null;
  }

  document.querySelectorAll('[data-confirm-form]').forEach(function (form) {
    form.addEventListener('submit', function (event) {
      if (form.dataset.confirmed === 'true') {
        delete form.dataset.confirmed;
        return;
      }

      event.preventDefault();
      pendingForm = form;
      pendingSubmitter = event.submitter || null;
      priorFocus = document.activeElement;
      title.textContent = form.dataset.confirmTitle || 'Confirm action?';
      description.textContent = form.dataset.confirmDescription || 'This action requires confirmation.';
      acceptButton.textContent = form.dataset.confirmLabel || 'Confirm';
      document.body.classList.add('confirm-open');
      dialog.showModal();
      cancelButton.focus();
    });
  });

  cancelButton.addEventListener('click', closeDialog);

  acceptButton.addEventListener('click', function () {
    if (!pendingForm) return;

    var form = pendingForm;
    var submitter = pendingSubmitter;
    var confirmationField = form.querySelector('[data-confirmation-field]');
    if (confirmationField) confirmationField.value = form.dataset.confirmValue || 'confirm';
    form.dataset.confirmed = 'true';
    dialog.close();
    if (submitter) form.requestSubmit(submitter);
    else form.requestSubmit();
  });

  dialog.addEventListener('click', function (event) {
    if (event.target === dialog) closeDialog();
  });

  dialog.addEventListener('close', clearDialogState);
})();

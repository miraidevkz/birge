// Body of the Kratos HTTP courier call to the Birge courier hook
// (authgw.CourierHook on the worker's internal listener). Only the number,
// the template type and the code leave Kratos: never the identity, its traits
// or the rendered body. The hook refuses every template but the login and
// registration code, so any other message fails there.
function(ctx) {
  to: ctx.recipient,
  template: ctx.template_type,
  code:
    if std.objectHas(ctx.template_data, 'login_code') then ctx.template_data.login_code
    else if std.objectHas(ctx.template_data, 'registration_code') then ctx.template_data.registration_code
    else '',
}

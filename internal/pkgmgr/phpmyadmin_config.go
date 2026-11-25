package pkgmgr

// 配置只由面板网关注入，直接交给其它Web服务执行时拒绝访问。
const PHPMyAdminConfig = `<?php
$panel = json_decode(base64_decode(getenv('EZPANEL_PMA_CONFIG') ?: ''), true);
if (!is_array($panel) || empty($panel['base']) || empty($panel['user'])) {
    http_response_code(403);
    exit('Please open phpMyAdmin from ezpanel.');
}
$cfg['PmaAbsoluteUri'] = $panel['base'];
$cfg['blowfish_secret'] = $panel['secret'];
$cfg['TempDir'] = $panel['temp'];
$cfg['AllowArbitraryServer'] = false;
// 管理窗口与面板同源，仍禁止其它网站嵌入。
$cfg['AllowThirdPartyFraming'] = 'sameorigin';
$cfg['AuthLog'] = 'sapi';
$cfg['Servers'][1] = [
    'auth_type' => 'signon',
    'SignonScript' => __DIR__ . '/ezpanel-signon.php',
    'SignonURL' => $panel['base'],
    'LogoutURL' => $panel['logout'],
    'host' => $panel['host'],
    'port' => $panel['port'],
    'socket' => $panel['socket'],
    'connect_type' => $panel['socket'] === '' ? 'tcp' : 'socket',
    'only_db' => [$panel['database']],
    'ssl' => $panel['ssl'],
    'ssl_verify' => $panel['ssl'],
    'ssl_ca' => $panel['ca'],
    'AllowNoPassword' => false,
    'hide_connection_errors' => true,
];
$cfg['ServerDefault'] = 1;
unset($panel);
`

const PHPMyAdminSignon = `<?php
function get_login_credentials($user) {
    $panel = json_decode(base64_decode(getenv('EZPANEL_PMA_CONFIG') ?: ''), true);
    if (!is_array($panel)) {
        return ['', ''];
    }
    return [$panel['user'], $panel['password']];
}
`

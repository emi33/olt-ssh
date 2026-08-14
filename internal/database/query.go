package database

// rc.id se trae para poder escribir de vuelta el índice de service-port
// asignado en la OLT (columna sp_index) sobre la fila exacta — ver
// GuardarSPIndex.
//
// queryRegistrosProvisioning obtiene los registros de clientes activos en CT,
// cruzando observacionesct con eqcliente (ultima fila por MAC), conexiones y onu.
// Solo se traen MACs validas de clientes activos (ct.activo = 1) con fecha >= hoy
// (CURDATE()) para la OLT indicada. El idOlt de eqcliente entra como parametro
// posicional (?), desde PROVISIONING_ID_OLT (ver config). El cruce con onu es por
// idOlt+puertoOlt+identificador (no por MAC). Los resultados se filtran en Go por
// plan/estado antes de ejecutar en OLT.
const queryRegistrosProvisioning = `
SELECT
    rc.id,
    rc.mac_wan,
    rc.pppoe AS cliente,
    rc.ip_cliente,
    rc.ip_controlador AS ipCT,
    IF(rc.nro_cliente IS NULL, NULL, CAST(rc.nro_cliente AS CHAR)) AS nroConexion,
    rc.vlan,
    rc.ont_id AS identificador,
    rc.puerto AS puertoOlt,
    rc.gem_id AS gemId,
    rc.idx AS sPort,
    rc.plan,
    ro.sn,
    ro.ont_id AS onu_identificador,
    '' AS streamProfile,
    '' AS serviceProfile,
    ro.estado_presencia,
    ro.cantidad_macs,
    rc.estado_registro
FROM olt_test.registro_cliente rc
JOIN olt_test.registro_onu ro
  ON ro.id_olt = rc.id_olt
 AND ro.puerto = rc.puerto
 AND ro.ont_id = rc.ont_id
WHERE rc.id_olt = ?
  AND rc.listo_para_cargar = 1
  AND ro.listo_para_cargar = 1
ORDER BY rc.puerto, rc.ont_id, rc.vlan, rc.mac_wan
`

// queryCargaPrincipal es la "consulta principal" del flujo de carga a las tablas
// de registro (registro_onu / registro_cliente). Trae los clientes ACTIVOS en los
// CT concentradores, cruzados con la última aparición de cada MAC en eqcliente
// (rn=1) y su ONU. Incluye eq.idx (trazabilidad del índice Kingtype).
//
// Placeholders posicionales: (idOlt del subquery, fecha de corte, ...ipCT). El
// `%s` se reemplaza en Go por la lista de '?' de la cláusula IN (controladores
// dinámicos desde PROVISIONING_CONTROLADORES).
const queryCargaPrincipal = `
SELECT
    ct.mac              AS mac_wan,
    ct.cliente          AS pppoe,
    ct.ipCliente,
    ct.ipCT             AS ipControlador,
    ct.fecha            AS fechaCT,
    c.nroConexion       AS nroCliente,
    eq.vlan,
    eq.puertoOlt        AS puertoPon,
    eq.identificador    AS onu_id,
    eq.idx              AS indice,
    ct.activo           AS esActivo,
    eq.fechaHora        AS fechaEq,
    c.plan,
    onu.sn,
    onu.identificador   AS onu_identificador,
    onu.estadoPresencia,
    onu.cantidadmacs
FROM observacionesct ct
JOIN (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY mac ORDER BY fechaHora DESC) AS rn
    FROM olt_test.eqcliente
    WHERE idOlt = ?
) eq ON UPPER(eq.mac) = UPPER(ct.mac) AND eq.rn = 1
LEFT JOIN conexiones c     ON ct.cliente = c.pppoe
LEFT JOIN olt_test.onu onu ON onu.idOlt = eq.idOlt
                          AND onu.puertoOlt = eq.puertoOlt
                          AND onu.identificador = eq.identificador
                          AND onu.mac = eq.mac
WHERE ct.mac IS NOT NULL AND ct.mac != ''
  AND ct.fecha >= ?
  AND ct.activo = 1
  AND ct.ipCT IN (%s)
`

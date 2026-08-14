-- ---------------------------------------------------------------------------
-- Migración: soporte multi-vlan en registro_cliente
-- ---------------------------------------------------------------------------
-- Motivo: el PK de eqcliente es
--     (idOlt, puertoOlt, identificador, mac, vlan, fechaHora)
-- o sea que la vlan es parte de la identidad de la fila: una misma MAC con dos
-- vlans son DOS servicios distintos, cada uno con su propio idx. El repaso
-- resolvía las macs con una ventana `PARTITION BY mac` y se quedaba con rn=1,
-- así que de cada MAC sobrevivía un solo servicio y el resto se perdía en
-- silencio. Ahora la ventana parte por (mac, vlan) y emite una fila por
-- servicio, lo que exige que registro_cliente pueda guardar la misma mac_wan
-- más de una vez.
--
-- Correr ANTES de desplegar el código nuevo: con el uq_olt_mac viejo, la
-- segunda fila de cada MAC pisaría a la primera vía ON DUPLICATE KEY UPDATE y
-- el trabajo quedaría invisible.
--
-- No hace falta limpiar a mano: el repaso borra y reescribe las tablas de la
-- OLT en una transacción, así que alcanza con volver a correrlo.
-- ---------------------------------------------------------------------------

-- 1) Índices únicos.
--    uq_olt_mac (id_olt, mac_wan) hacía imposible guardar dos servicios de la
--    misma MAC. Pasa a incluir la vlan: la clave natural es el SERVICIO.
--    No se incluye idx: es NULLable y MySQL admite NULLs repetidos en un índice
--    único, así que dejaría entrar duplicados justo cuando el idx viene vacío.
--
--    uq_olt_pppoe (id_olt, pppoe) se elimina: observacionesct mapea mac ->
--    cliente SIN vlan y el join es por MAC sola, así que un cliente cuya MAC
--    presta dos servicios queda atribuido a las dos filas (mismo pppoe,
--    distinta vlan). Es inherente a los datos de origen. De paso, permite que
--    un mismo cliente aparezca con dos MACs distintas, que antes se bloqueaba.
ALTER TABLE olt_test.registro_cliente
  DROP INDEX uq_olt_mac,
  DROP INDEX uq_olt_pppoe,
  ADD UNIQUE KEY uq_olt_mac_vlan (id_olt, mac_wan, vlan);

-- 2) Blindaje de la vlan.
--    vlan entra en el índice único y era NULLable; como MySQL permite NULLs
--    repetidos en un índice único, una vlan NULL abriría la puerta a duplicados
--    exactos de (id_olt, mac_wan). Go siempre escribe un entero
--    (RegistroCliente.Vlan es int, no nullable), así que en la práctica nunca es
--    NULL — esto lo hace explícito a nivel schema.
ALTER TABLE olt_test.registro_cliente
  MODIFY vlan INT NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- Verificación posterior: qué MACs quedaron con más de un servicio.
-- ---------------------------------------------------------------------------
-- SELECT mac_wan, vlan, idx, pppoe, estado_registro
-- FROM olt_test.registro_cliente
-- WHERE id_olt = 2 AND mac_wan IN (
--     SELECT mac_wan FROM olt_test.registro_cliente
--     WHERE id_olt = 2 GROUP BY mac_wan HAVING COUNT(*) > 1)
-- ORDER BY mac_wan, vlan
-- LIMIT 30;

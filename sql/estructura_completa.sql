-- ============================================================================
-- olt_test — ESTRUCTURA COMPLETA (solo DDL)
-- ============================================================================
-- Todas las tablas del schema `olt_test`, sin datos y sin filas de catálogo.
-- Generado desde la base viva; recrea el schema vacío desde cero.
--
-- Orden de creación: las tablas referenciadas van primero, para que las FOREIGN
-- KEY resuelvan sin desactivar los chequeos.
--
-- Uso:
--   mysql -u root -p < sql/estructura_completa.sql
-- ============================================================================

-- `conexiones` viene de la app legacy y define DEFAULT '0000-00-00 00:00:00' en
-- dos columnas datetime. El sql_mode por defecto de MySQL 8 (NO_ZERO_DATE) lo
-- rechaza con "Invalid default value". Se afloja el modo solo para esta sesión
-- y se restaura al final, para conservar el DDL tal cual está en producción.
SET @OLD_SQL_MODE = @@SESSION.sql_mode;
SET SESSION sql_mode = 'NO_ENGINE_SUBSTITUTION';

CREATE DATABASE IF NOT EXISTS `olt_test`
  DEFAULT CHARACTER SET utf8mb4
  DEFAULT COLLATE utf8mb4_0900_ai_ci;

USE `olt_test`;


-- ----------------------------------------------------------------------------
-- olt
-- ----------------------------------------------------------------------------
-- Inventario de OLTs. `ipAdmin` es la IP de gestión que el guardrail de
-- cmd/provisionar compara contra OLT_HOST antes de escribir.

CREATE TABLE IF NOT EXISTS `olt` (
  `idOlt` int NOT NULL AUTO_INCREMENT,
  `descripcion` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `ipAdmin` varchar(45) COLLATE utf8mb4_unicode_ci NOT NULL,
  `mac` varchar(17) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `cantPuertos` int DEFAULT NULL,
  `marca` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `modelo` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `firmWare` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `usuario` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `pass` varchar(100) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `puertoSSH` int DEFAULT '22',
  PRIMARY KEY (`idOlt`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- vlan
-- ----------------------------------------------------------------------------
-- Catálogo de VLANs de servicio.

CREATE TABLE IF NOT EXISTS `vlan` (
  `idVlan` int NOT NULL,
  `descripcion` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  PRIMARY KEY (`idVlan`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- planes
-- ----------------------------------------------------------------------------
-- Planes comerciales. `codigo_trafico` es el valor que va en
-- traffic-in / traffic-out del service-port.

CREATE TABLE IF NOT EXISTS `planes` (
  `id` int NOT NULL AUTO_INCREMENT,
  `nombre` varchar(50) COLLATE utf8mb4_unicode_ci NOT NULL,
  `velocidad_mb` int DEFAULT NULL,
  `tipo` enum('hogar','pyme','otro') COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'hogar',
  `codigo_trafico` int NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_nombre` (`nombre`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- gem_config
-- ----------------------------------------------------------------------------
-- Configuración GEM por VLAN (T-CONT + gem_port + gem_mapping unificados).

CREATE TABLE IF NOT EXISTS `gem_config` (
  `id_olt` int NOT NULL,
  `tcont_id` int NOT NULL,
  `gem_port_id` int NOT NULL,
  `gem_mapping_id` int NOT NULL,
  `vlan` int NOT NULL,
  `prioridad` int DEFAULT NULL,
  `port` int DEFAULT NULL,
  PRIMARY KEY (`id_olt`,`gem_port_id`,`gem_mapping_id`),
  UNIQUE KEY `uq_olt_vlan` (`id_olt`,`vlan`),
  KEY `idx_vlan` (`id_olt`,`vlan`),
  KEY `idx_tcont` (`id_olt`,`tcont_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- onu
-- ----------------------------------------------------------------------------
-- ONUs leídas de la OLT por read-olt: SN, MAC y estadoPresencia.

CREATE TABLE IF NOT EXISTS `onu` (
  `idOlt` int NOT NULL,
  `puertoOlt` int NOT NULL,
  `identificador` int NOT NULL,
  `sn` varchar(50) COLLATE utf8mb4_unicode_ci NOT NULL,
  `mac` varchar(17) COLLATE utf8mb4_unicode_ci NOT NULL,
  `ready` int DEFAULT '0',
  `status` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT 'unknown',
  `estadoPresencia` varchar(20) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'presente' COMMENT 'presente=encontrado en la ultima corrida de register-info de su OLT, ausente=ya no aparece (posible baja fisica), nunca se borra la fila',
  `streamProfile` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `serviceProfile` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `ultima_actualizacion` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `cantidadMacs` int NOT NULL DEFAULT '0',
  PRIMARY KEY (`idOlt`,`puertoOlt`,`identificador`),
  CONSTRAINT `fk_onu_olt` FOREIGN KEY (`idOlt`) REFERENCES `olt` (`idOlt`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- eqcliente
-- ----------------------------------------------------------------------------
-- Equipos de cliente vistos en la OLT: MAC, VLAN, puerto, identificador e idx.

CREATE TABLE IF NOT EXISTS `eqcliente` (
  `idOlt` int NOT NULL,
  `vlan` int NOT NULL,
  `mac` varchar(17) COLLATE utf8mb4_unicode_ci NOT NULL,
  `fechaHora` datetime NOT NULL,
  `identificador` int NOT NULL,
  `puertoOlt` int NOT NULL,
  `idx` int DEFAULT NULL,
  `gemId` int DEFAULT NULL,
  `sPort` int DEFAULT NULL,
  PRIMARY KEY (`idOlt`,`puertoOlt`,`identificador`,`mac`,`vlan`,`fechaHora`),
  KEY `fk_eq_vlan` (`vlan`),
  KEY `fk_cliente_onu` (`idOlt`,`puertoOlt`,`identificador`),
  CONSTRAINT `fk_cliente_onu` FOREIGN KEY (`idOlt`, `puertoOlt`, `identificador`) REFERENCES `onu` (`idOlt`, `puertoOlt`, `identificador`) ON DELETE CASCADE,
  CONSTRAINT `fk_eq_olt` FOREIGN KEY (`idOlt`) REFERENCES `olt` (`idOlt`) ON DELETE CASCADE,
  CONSTRAINT `fk_eq_vlan` FOREIGN KEY (`vlan`) REFERENCES `vlan` (`idVlan`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- observacionesct
-- ----------------------------------------------------------------------------
-- Observaciones crudas de los controladores (CT): MAC, cliente, IP, activo.

CREATE TABLE IF NOT EXISTS `observacionesct` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `fecha` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `ipCT` varchar(50) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci NOT NULL,
  `cliente` varchar(150) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci NOT NULL,
  `nroConexion` int DEFAULT NULL,
  `ipCliente` varchar(50) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `tieneCola` tinyint(1) NOT NULL DEFAULT '0',
  `tienePPPoE` tinyint(1) NOT NULL DEFAULT '0',
  `activo` tinyint(1) NOT NULL DEFAULT '0',
  `password` varchar(255) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `mac` varchar(12) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `limit_at` varchar(50) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `max_limit` varchar(50) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `priority` varchar(50) CHARACTER SET utf8mb3 COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `cortado` tinyint(1) NOT NULL DEFAULT '0',
  PRIMARY KEY (`id`),
  KEY `cliente` (`cliente`),
  KEY `nroConexion` (`nroConexion`),
  KEY `ipCT` (`ipCT`),
  KEY `fecha` (`fecha`),
  KEY `idx_ipCT_fecha` (`ipCT`,`fecha`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb3 COLLATE=utf8mb3_spanish_ci;


-- ----------------------------------------------------------------------------
-- conexiones
-- ----------------------------------------------------------------------------
-- Clientes / conexiones: pppoe, nroConexion y plan.
--
-- ⚠️ La tabla original tiene una FOREIGN KEY `fkConexionesVecino`
--    (nroConexion -> vecino.idvecino), pero `vecino` NO existe en este
--    schema (vive en `prueba`). Queda comentada dentro del CREATE para
--    que el script corra sobre una base limpia; si migrás `vecino` a
--    olt_test, descomentala.

CREATE TABLE IF NOT EXISTS `conexiones` (
  `idConexiones` int NOT NULL AUTO_INCREMENT,
  `nroConexion` int unsigned NOT NULL,
  `idCliente` varchar(11) CHARACTER SET utf8mb3 COLLATE utf8mb3_bin NOT NULL DEFAULT '0',
  `idPersona` int NOT NULL DEFAULT '0',
  `idInstalador` int DEFAULT '0',
  `idContrato` int NOT NULL DEFAULT '0',
  `plan` varchar(50) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `idPlan` int NOT NULL DEFAULT '0',
  `estado` int NOT NULL DEFAULT '0' COMMENT 'Uno de los valores definidos en la tabla conexionEstados.\nNo tiene definida ForeignKey, por ahora la corrección de su valor es responsabilidad de la app en CodeIgniter.',
  `bandera` int NOT NULL DEFAULT '1',
  `direccion` varchar(200) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `idDistrito` int DEFAULT '0',
  `idDepartamento` int DEFAULT '0',
  `departamento` varchar(50) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `distrito` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `provincia` varchar(50) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `coordenadas` varchar(50) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `padronMunicipal` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `idEquipoCliente` int NOT NULL DEFAULT '0',
  `ipCliente` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `ipSuscriptor` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `idEquipoSuscriptor` int NOT NULL DEFAULT '0',
  `idNodo` int NOT NULL DEFAULT '0',
  `detalleNodo` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `puertoOLT` int DEFAULT NULL,
  `ipNodo` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `idEquipoCT` int DEFAULT NULL,
  `ipCT` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `ipPublico` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `puertoIPpublico` int DEFAULT '0',
  `nombreCola` varchar(100) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `limit_at` varchar(20) COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `max_limit` varchar(20) COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `priority` varchar(10) COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `PPPoE` varchar(100) COLLATE utf8mb3_spanish_ci NOT NULL DEFAULT '1',
  `pass` varchar(30) COLLATE utf8mb3_spanish_ci DEFAULT '?',
  `bytesDescargados` bigint DEFAULT '0',
  `vlanA` int NOT NULL DEFAULT '1',
  `vlanB` int NOT NULL DEFAULT '1',
  `vlanC` int NOT NULL DEFAULT '1',
  `macGPONcliente` varchar(16) COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `idComponente` int DEFAULT NULL,
  `ontConfig` varchar(100) COLLATE utf8mb3_spanish_ci DEFAULT NULL,
  `fechaAlta` date DEFAULT NULL,
  `fechaInstalacion` date DEFAULT NULL,
  `fechaCorte` datetime DEFAULT NULL,
  `fechaHabPosCorte` datetime DEFAULT NULL,
  `idDescuentoBono` int DEFAULT NULL,
  `legajoDescuentoBono` int DEFAULT NULL,
  `importado` tinyint DEFAULT '0',
  `triplicado` int DEFAULT '0' COMMENT '0-No, 1-Si',
  `solicitoTriplica` int DEFAULT '0' COMMENT '0-No, 1-Si',
  `idUsuarioCreador` int NOT NULL DEFAULT '0',
  `creado` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `ultimoCambio` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',
  `borrado` datetime DEFAULT NULL,
  `ultimaVerifCT` datetime DEFAULT NULL COMMENT 'Fecha de última verificación SATISFACTORIA contra los controladores de tráfico, se realiza con el programa NAVEGANTES que además genera un reporte con las diferencias.',
  PRIMARY KEY (`idConexiones`),
  UNIQUE KEY `conexion_nroConexion` (`nroConexion`),
  KEY `idPersona` (`idPersona`),
  KEY `idUsuarioCreador` (`idUsuarioCreador`),
  KEY `idPlan` (`idPlan`)
  -- CONSTRAINT `fkConexionesVecino` FOREIGN KEY (`nroConexion`) REFERENCES `vecino` (`idvecino`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb3 COLLATE=utf8mb3_spanish_ci;


-- ----------------------------------------------------------------------------
-- comodatosnico
-- ----------------------------------------------------------------------------
-- Datos de comodato de la migración Nico (scoring y conciliación).
-- No la usa ningún comando de olt-ssh-go; se incluye por completitud.

CREATE TABLE IF NOT EXISTS `comodatosnico` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `id_migracion` int NOT NULL,
  `contrato_nico` varchar(50) DEFAULT NULL,
  `idvecino` int DEFAULT NULL,
  `score` decimal(4,3) NOT NULL,
  `nivel_confianza` varchar(10) NOT NULL,
  `conflicto` tinyint(1) NOT NULL DEFAULT '0',
  `pppoe` varchar(150) DEFAULT NULL,
  `dni` varchar(20) DEFAULT NULL,
  `apellido` varchar(255) DEFAULT NULL,
  `nombre` varchar(255) DEFAULT NULL,
  `cuil` varchar(20) DEFAULT NULL,
  `razon_social` varchar(255) DEFAULT NULL,
  `correo_persona` varchar(100) DEFAULT NULL,
  `conexion_direccion` varchar(255) DEFAULT NULL,
  `fecha_conexion` date DEFAULT NULL,
  `persona_direccion` varchar(255) DEFAULT NULL,
  `empresa_direccion` varchar(255) DEFAULT NULL,
  `observacion` text,
  `mac` varchar(50) DEFAULT NULL,
  `num_serie` varchar(100) DEFAULT NULL,
  `marca` varchar(50) DEFAULT NULL,
  `modelo` varchar(50) DEFAULT NULL,
  `vecino_apenom` varchar(255) DEFAULT NULL,
  `vecino_cuil` varchar(20) DEFAULT NULL,
  `vecino_domicilio` varchar(255) DEFAULT NULL,
  `vecino_correo` varchar(100) DEFAULT NULL,
  `fecha_proceso` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `reglas_aplicadas` text,
  PRIMARY KEY (`id`),
  KEY `idx_migracion` (`id_migracion`),
  KEY `idx_vecino` (`idvecino`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;


-- ----------------------------------------------------------------------------
-- registro_onu
-- ----------------------------------------------------------------------------
-- Tabla de registro de ONUs: la fuente de los comandos `ont add`.
-- Solo se cargan las filas con listo_para_cargar = 1.

CREATE TABLE IF NOT EXISTS `registro_onu` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `id_olt` int NOT NULL,
  `puerto` int NOT NULL,
  `ont_id` int NOT NULL,
  `sn` varchar(20) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `estado_presencia` varchar(20) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `cantidad_macs` int NOT NULL DEFAULT '0',
  `desc_ont` varchar(100) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `estado_registro` varchar(60) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'principal_pendiente',
  `listo_para_cargar` tinyint(1) NOT NULL DEFAULT '0',
  `motivo` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `fecha_alta` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `fecha_actualizacion` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `fecha_registrada` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_olt_puerto_ont` (`id_olt`,`puerto`,`ont_id`),
  UNIQUE KEY `uq_regonu_olt_sn` (`id_olt`,`sn`),
  KEY `idx_regonu_estado` (`id_olt`,`estado_registro`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


-- ----------------------------------------------------------------------------
-- registro_cliente
-- ----------------------------------------------------------------------------
-- Tabla de registro de clientes: la fuente de los comandos `service-port`.
-- Una fila por (ONU, VLAN); se cargan las que tienen listo_para_cargar = 1.

CREATE TABLE IF NOT EXISTS `registro_cliente` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `id_olt` int NOT NULL,
  `puerto` int NOT NULL,
  `ont_id` int NOT NULL,
  `mac_wan` varchar(17) COLLATE utf8mb4_unicode_ci NOT NULL,
  `pppoe` varchar(100) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `vlan` int DEFAULT NULL,
  `plan_id` int DEFAULT NULL,
  `plan` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `gem_id` int DEFAULT NULL,
  `idx` int DEFAULT NULL,
  `fecha_eq` datetime DEFAULT NULL,
  `sp_index` int DEFAULT NULL,
  `nro_cliente` int DEFAULT NULL,
  `ip_cliente` varchar(45) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `ip_controlador` varchar(45) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `estado_registro` varchar(60) COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'principal_pendiente',
  `listo_para_cargar` tinyint(1) NOT NULL DEFAULT '0',
  `motivo` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `fecha_alta` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `fecha_actualizacion` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `fecha_creado` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_olt_mac_vlan` (`id_olt`,`mac_wan`,`vlan`),
  KEY `idx_regcli_onu` (`id_olt`,`puerto`,`ont_id`),
  KEY `idx_regcli_estado` (`id_olt`,`estado_registro`),
  KEY `fk_regcliente_plan` (`plan_id`),
  CONSTRAINT `fk_regcliente_plan` FOREIGN KEY (`plan_id`) REFERENCES `planes` (`id`),
  CONSTRAINT `fk_regcliente_regonu` FOREIGN KEY (`id_olt`, `puerto`, `ont_id`) REFERENCES `registro_onu` (`id_olt`, `puerto`, `ont_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;


SET SESSION sql_mode = @OLD_SQL_MODE;

-- ============================================================================
-- Fin. Sin INSERTs: las tablas de catálogo (`planes`, `gem_config`, `vlan`,
-- `olt`) quedan vacías y hay que poblarlas antes de correr cmd/cargar.
-- ============================================================================

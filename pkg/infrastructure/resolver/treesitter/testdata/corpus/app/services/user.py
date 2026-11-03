"""Every Python import form and top-level declaration kind."""
from __future__ import annotations

import os
import os.path
import json as j, sys
from ..models import User, Base
from . import helper as h
from .helper import thing
from app.services import helper
from app import models
from .. import models as m
from typing import (
    Any,
    Optional,
)
import numpy as np


class UserService(Base):
    def method(self):
        pass

    class Inner:
        pass


@staticmethod
def Helper():
    def nested():
        pass


@dataclass
class Point:
    x: int


async def fetch():
    pass


def _private():
    pass


class _Hidden:
    pass


CONSTANT = 1

if True:
    def conditional():
        pass


x = "日本語😀"; y = 1
def 名前():
    pass
